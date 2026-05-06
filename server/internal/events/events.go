// Package events serves the Browse and Hosted-creation flows. Other writes
// (commit, withdraw, …) live in their own packages.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

const (
	defaultRadiusM = 10000
	defaultFuzzM   = 200
	maxStartWindow = 72 * time.Hour
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
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	StartTime   time.Time `json:"start_time"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	Cap         *int      `json:"cap"`
	CommitCount int       `json:"commit_count"`
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
			(SELECT count(*) FROM public.commits cc WHERE cc.event_id = e.id) AS commit_count
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
		ORDER BY e.start_time ASC
	`, lat, lon, userID, radiusM)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query events: "+err.Error())
		return
	}
	defer rows.Close()

	out := make([]nearbyEvent, 0)
	for rows.Next() {
		var e nearbyEvent
		if err := rows.Scan(
			&e.ID, &e.Title, &e.Description, &e.Category, &e.StartTime,
			&e.Lat, &e.Lon, &e.Cap, &e.CommitCount,
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// createEventRequest is the JSON body of POST /events.
type createEventRequest struct {
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Category           string    `json:"category"`
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	Lat                float64   `json:"lat"`
	Lon                float64   `json:"lon"`
	Cap                *int      `json:"cap"`
	LocationVisibility string    `json:"location_visibility"`
}

// createdEvent is returned to the Host after a successful POST /events.
// The Host sees the exact pin (geom), not display_geom — fuzzing is for
// non-Attendee viewers via GET /events?near.
type createdEvent struct {
	ID                 string    `json:"id"`
	HostID             string    `json:"host_id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Category           string    `json:"category"`
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	Cap                *int      `json:"cap"`
	Lat                float64   `json:"lat"`
	Lon                float64   `json:"lon"`
	FuzzRadiusM        int       `json:"fuzz_radius_m"`
	LocationVisibility string    `json:"location_visibility"`
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
	return nil
}

// Create handles POST /events. Inserts an α-Event for the authenticated user
// and returns the new row. display_geom is computed once server-side as a
// uniform-random point within fuzz_radius_m of geom (PRD §Geo data model);
// the set-once invariant is enforced by INSERT-only writes — this row's
// display_geom is never recomputed after creation.
//
// β-Events / Tip threshold (#32) and rep-/friends-gating (#38, future)
// arrive in later slices; this slice is α-only.
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

	row := h.pool.QueryRow(r.Context(), `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, display_geom, fuzz_radius_m, location_visibility
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
			$10
		)
		RETURNING
			id, host_id, title, description, category,
			start_time, end_time, cap,
			ST_Y(geom)::float8, ST_X(geom)::float8,
			fuzz_radius_m, location_visibility
	`,
		hostID, in.Title, in.Description, in.Category,
		in.StartTime, in.EndTime, in.Cap,
		in.Lon, in.Lat,
		visibility, defaultFuzzM,
	)

	var out createdEvent
	if err := row.Scan(
		&out.ID, &out.HostID, &out.Title, &out.Description, &out.Category,
		&out.StartTime, &out.EndTime, &out.Cap,
		&out.Lat, &out.Lon,
		&out.FuzzRadiusM, &out.LocationVisibility,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert event: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(out)
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
