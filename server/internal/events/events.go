// Package events serves the Browse and Hosted-creation flows. Other writes
// (commit, withdraw, …) live in their own packages.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

const (
	defaultRadiusM = 10000
	defaultFuzzM   = 200
	maxStartWindow = 72 * time.Hour
	defaultWindow  = 72 * time.Hour
	// 50ms is the budget for the SQL+pgx leg of a discovery-feed query
	// that fires on every UI gesture: end-to-end is sub-100ms (sub-1ms
	// warm today), and anything slower than 50ms is signal — likely host
	// resource starvation, a missing index, or a plan flip — not noise.
	browseSlowThreshold = 50 * time.Millisecond
	// β-Event: deadline must precede start_time by at least this margin
	// so the Tip transition has a meaningful "is the Event happening?"
	// answer before Attendees are en route. Per PRD §Event creation.
	tipDeadlineMargin = 15 * time.Minute
)

var validCategories = map[string]bool{
	"Sports": true, "Food/Drink": true, "Music": true, "Outdoors": true,
	"Games": true, "Social": true, "Creative": true, "Wellness": true,
	"Networking": true, "Other": true,
}

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type nearbyEvent struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Category      string    `json:"category"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Lat           float64   `json:"lat"`
	Lon           float64   `json:"lon"`
	Cap           *int      `json:"cap"`
	CommitCount   int       `json:"commit_count"`
	CommittedByMe bool      `json:"committed_by_me"`
	State         string    `json:"state"`
}

// Near handles GET /events?near=lat,lon&radius_m=…
//
// Coordinate-visibility branches per PRD-v0 §Geo data model / CONTEXT.md §Location
// fuzzing:
//   - location_visibility='public'  → exact center for all viewers
//   - location_visibility='fuzzed' + viewer NOT Committed → display_geom
//   - location_visibility='fuzzed' + viewer Committed     → exact center
//
// Past Events (end_time ≤ now) are excluded server-side.
func (h *Handler) Near(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	lat, lon, err := parseNear(r.URL.Query().Get("near"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	radiusM, err := parseRadius(r.URL.Query().Get("radius_m"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to, err := parseWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tQuery := time.Now()
	// Geographic filter is on the *true* geom, not display_geom: a viewer
	// "near" an Event should see it regardless of which pin we end up
	// returning, otherwise non-Committed viewers near the true center would
	// miss fuzzed Events whose display pin happened to drift outside their
	// radius.
	rows, err := h.pool.Query(r.Context(), `
		SELECT
			e.id,
			e.title,
			e.description,
			e.category,
			e.start_time,
			e.end_time,
			CASE
				WHEN e.location_visibility = 'public' OR c.user_id IS NOT NULL
					THEN ST_Y(e.geom)
				ELSE ST_Y(e.display_geom)
			END AS lat,
			CASE
				WHEN e.location_visibility = 'public' OR c.user_id IS NOT NULL
					THEN ST_X(e.geom)
				ELSE ST_X(e.display_geom)
			END AS lon,
			e.cap,
			(SELECT count(*) FROM public.commits cc WHERE cc.event_id = e.id) AS commit_count,
			(c.user_id IS NOT NULL) AS committed_by_me,
			public.event_state(e) AS state
		FROM public.events e
		LEFT JOIN public.commits c
			ON c.event_id = e.id AND c.user_id = $3
		WHERE
			ST_DWithin(
				e.geom::geography,
				ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
				$4
			)
			AND e.end_time > now()
			AND e.start_time < $6
			AND e.end_time > $5
		ORDER BY e.start_time ASC
	`, lat, lon, userID, radiusM, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query events: "+err.Error())
		return
	}
	defer rows.Close()

	out := make([]nearbyEvent, 0)
	for rows.Next() {
		var e nearbyEvent
		if err := rows.Scan(
			&e.ID, &e.Title, &e.Description, &e.Category, &e.StartTime, &e.EndTime,
			&e.Lat, &e.Lon, &e.Cap, &e.CommitCount, &e.CommittedByMe, &e.State,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan: "+err.Error())
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows: "+err.Error())
		return
	}
	if dur := time.Since(tQuery); dur > browseSlowThreshold {
		log.Printf("near: slow query %s (rows=%d, threshold=%s) — investigate pool/host/plan",
			dur, len(out), browseSlowThreshold)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// createEventRequest is the JSON body of POST /events.
//
// α-Event: TipThreshold and TipDeadline both nil.
// β-Event: TipThreshold and TipDeadline both non-nil. The pair invariant
// is enforced in validate() and again at the DB (events_tip_pair).
type createEventRequest struct {
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Category           string     `json:"category"`
	StartTime          time.Time  `json:"start_time"`
	EndTime            time.Time  `json:"end_time"`
	Lat                float64    `json:"lat"`
	Lon                float64    `json:"lon"`
	Cap                *int       `json:"cap"`
	LocationVisibility string     `json:"location_visibility"`
	TipThreshold       *int       `json:"tip_threshold,omitempty"`
	TipDeadline        *time.Time `json:"tip_deadline,omitempty"`
}

// createdEvent is returned to the Host after a successful POST /events.
// The Host sees the exact pin (geom), not display_geom — fuzzing is for
// non-Attendee viewers via GET /events?near.
//
// TipThreshold and TipDeadline are echoed back when set so the iOS create
// sheet can display the confirmed β configuration without a follow-up read.
type createdEvent struct {
	ID                 string     `json:"id"`
	HostID             string     `json:"host_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Category           string     `json:"category"`
	StartTime          time.Time  `json:"start_time"`
	EndTime            time.Time  `json:"end_time"`
	Cap                *int       `json:"cap"`
	Lat                float64    `json:"lat"`
	Lon                float64    `json:"lon"`
	FuzzRadiusM        int        `json:"fuzz_radius_m"`
	LocationVisibility string     `json:"location_visibility"`
	TipThreshold       *int       `json:"tip_threshold,omitempty"`
	TipDeadline        *time.Time `json:"tip_deadline,omitempty"`
}

func (in *createEventRequest) validate(now time.Time) error {
	if strings.TrimSpace(in.Title) == "" {
		return errors.New("title is required")
	}
	if !validCategories[in.Category] {
		return fmt.Errorf("invalid category %q", in.Category)
	}
	if in.StartTime.Before(now) {
		return errors.New("start_time must not be in the past")
	}
	if in.StartTime.After(now.Add(maxStartWindow)) {
		return errors.New("start_time must be within 72 hours of now")
	}
	if !in.EndTime.After(in.StartTime) {
		return errors.New("end_time must be after start_time")
	}
	if in.Lat < -90 || in.Lat > 90 {
		return errors.New("lat out of range [-90, 90]")
	}
	if in.Lon < -180 || in.Lon > 180 {
		return errors.New("lon out of range [-180, 180]")
	}
	if in.Cap != nil && *in.Cap < 1 {
		return errors.New("cap must be ≥ 1")
	}
	switch in.LocationVisibility {
	case "", "fuzzed", "public":
	default:
		return fmt.Errorf("invalid location_visibility %q", in.LocationVisibility)
	}
	if (in.TipThreshold == nil) != (in.TipDeadline == nil) {
		return errors.New("tip_threshold and tip_deadline must both be set (β-Event) or both omitted (α-Event)")
	}
	if in.TipThreshold != nil {
		if *in.TipThreshold < 2 {
			return errors.New("tip_threshold must be ≥ 2")
		}
		if in.Cap != nil && *in.Cap < *in.TipThreshold {
			return errors.New("cap must be ≥ tip_threshold")
		}
		if !in.TipDeadline.After(now) {
			return errors.New("tip_deadline must be in the future")
		}
		if in.TipDeadline.After(in.StartTime.Add(-tipDeadlineMargin)) {
			return fmt.Errorf("tip_deadline must be at least %s before start_time", tipDeadlineMargin)
		}
	}
	return nil
}

// Create handles POST /events. Inserts an α- or β-Event for the authenticated
// user and returns the new row. display_geom is computed once server-side as
// a uniform-random point within fuzz_radius_m of geom (PRD §Geo data model);
// the set-once invariant is enforced by INSERT-only writes — this row's
// display_geom is never recomputed after creation.
//
// α vs β: a β-Event carries tip_threshold + tip_deadline; an α-Event carries
// neither. The Tip transition (sets tipped_at) lives in the Commit handler;
// auto-cancel of un-Tipped β-Events past their deadline lives in the
// lifecycle background loop. Rep-/friends-gating arrives in #38.
//
// Seeder auto-commit: a β-Event creator is auto-Committed in the same
// transaction as the INSERT — the Seeder is presumed to attend their own
// proposal, and the auto-Commit counts toward tip_threshold (PRD §Event
// creation reads "Tag in Echo Park, 6 needed" as 6 total Commits, the
// Seeder being one of them). α creators are not auto-Committed — Hosts run
// the event without occupying a cap slot.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	hostID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	var in createEventRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := in.validate(time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	visibility := in.LocationVisibility
	if visibility == "" {
		visibility = "fuzzed"
	}

	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, display_geom, fuzz_radius_m, location_visibility,
			tip_threshold, tip_deadline
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			ST_SetSRID(ST_MakePoint($8, $9), 4326),
			CASE WHEN $10 = 'fuzzed' THEN
				ST_Project(
					ST_SetSRID(ST_MakePoint($8, $9), 4326)::geography,
					$11 * sqrt(random()),
					random() * 2 * pi()
				)::geometry
			ELSE NULL END,
			$11,
			$10,
			$12, $13
		)
		RETURNING
			id, host_id, title, description, category,
			start_time, end_time, cap,
			ST_Y(geom)::float8, ST_X(geom)::float8,
			fuzz_radius_m, location_visibility,
			tip_threshold, tip_deadline
	`,
		hostID, in.Title, in.Description, in.Category,
		in.StartTime, in.EndTime, in.Cap,
		in.Lon, in.Lat,
		visibility, defaultFuzzM,
		in.TipThreshold, in.TipDeadline,
	)

	var out createdEvent
	if err := row.Scan(
		&out.ID, &out.HostID, &out.Title, &out.Description, &out.Category,
		&out.StartTime, &out.EndTime, &out.Cap,
		&out.Lat, &out.Lon,
		&out.FuzzRadiusM, &out.LocationVisibility,
		&out.TipThreshold, &out.TipDeadline,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert event: "+err.Error())
		return
	}

	if in.TipThreshold != nil {
		if _, err := tx.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
			out.ID, hostID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "auto-commit seeder: "+err.Error())
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit tx: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(out)
}

// Cancel handles DELETE /events/{id}.
//
// Per ADR 0001, Seeders cannot cancel β-Events: the Tip mechanic is the
// commitment device, and a Seeder bailing after Commits land would defeat
// the point. The endpoint enforces this hard.
//
// α-Host cancel is a separate slice (see 0009_event_state.up.sql); this
// handler currently rejects α deletes with 501 so the route exists for
// the iOS create-sheet to call without a 404 surprise.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}
	eventID, ok := parseEventID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}

	var hostID string
	var tipThreshold *int
	err := h.pool.QueryRow(r.Context(),
		`SELECT host_id, tip_threshold FROM public.events WHERE id = $1`,
		eventID,
	).Scan(&hostID, &tipThreshold)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select event: "+err.Error())
		return
	}
	if hostID != userID {
		writeError(w, http.StatusForbidden, "not the event creator")
		return
	}
	if tipThreshold != nil {
		writeError(w, http.StatusForbidden, "seeders_cannot_cancel")
		return
	}
	writeError(w, http.StatusNotImplemented, "α-Host cancel not yet implemented")
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func parseEventID(r *http.Request) (string, bool) {
	raw := chi.URLParam(r, "id")
	if !uuidPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

func parseNear(raw string) (lat, lon float64, err error) {
	if raw == "" {
		return 0, 0, errors.New("near query param is required (format: lat,lon)")
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("near must be 'lat,lon'")
	}
	lat, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("near lat: %w", err)
	}
	lon, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("near lon: %w", err)
	}
	if lat < -90 || lat > 90 {
		return 0, 0, errors.New("near lat out of range [-90, 90]")
	}
	if lon < -180 || lon > 180 {
		return 0, 0, errors.New("near lon out of range [-180, 180]")
	}
	return lat, lon, nil
}

// parseWindow defaults missing bounds to [now, now+72h]. RFC3339 only —
// invalid input returns 400 to keep client/server time formats locked down.
func parseWindow(rawFrom, rawTo string, now time.Time) (time.Time, time.Time, error) {
	from := now
	to := now.Add(defaultWindow)
	if rawFrom != "" {
		t, err := time.Parse(time.RFC3339, rawFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
		from = t
	}
	if rawTo != "" {
		t, err := time.Parse(time.RFC3339, rawTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
		to = t
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}

func parseRadius(raw string) (int, error) {
	if raw == "" {
		return defaultRadiusM, nil
	}
	r, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("radius_m: %w", err)
	}
	if r <= 0 {
		return 0, errors.New("radius_m must be > 0")
	}
	return r, nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
