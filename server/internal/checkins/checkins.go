// Package checkins serves the check-in endpoint — the tap-driven Show
// signal an Attendee produces while at a Live Event (PRD §At-event,
// ADR 0011). Other write paths (Commit/Withdraw, create) live in their
// own packages.
package checkins

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

// geofenceFloorMeters is the accuracy-aware radius from ADR 0011: a tap
// is accepted iff distance_to_pin − horizontalAccuracy ≤ this floor.
const geofenceFloorMeters = 50.0

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type checkinRequest struct {
	Lat                 float64 `json:"lat"`
	Lon                 float64 `json:"lon"`
	HorizontalAccuracyM float64 `json:"horizontal_accuracy_m"`
}

type checkinResponse struct {
	RecordedAt time.Time `json:"recorded_at"`
}

type notAtEventResponse struct {
	Error     string  `json:"error"`
	DistanceM float64 `json:"distance_m"`
	AccuracyM float64 `json:"accuracy_m"`
}

// CheckIn handles POST /events/{id}/checkin.
//
// Order of checks matters: existence → committed-attendee → Live state →
// accuracy-aware distance. The order surfaces the most actionable error
// first (a non-Attendee can't fix being too far; a non-Live event makes
// distance moot). Telemetry logs every accepted/rejected attempt — Q7
// in PRD §Open questions calls out tuning the 50m floor post-launch.
func (h *Handler) CheckIn(w http.ResponseWriter, r *http.Request) {
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

	var in checkinRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if in.Lat < -90 || in.Lat > 90 {
		writeError(w, http.StatusBadRequest, "lat out of range [-90, 90]")
		return
	}
	if in.Lon < -180 || in.Lon > 180 {
		writeError(w, http.StatusBadRequest, "lon out of range [-180, 180]")
		return
	}
	// horizontal_accuracy_m is iOS's CLLocation.horizontalAccuracy. Negative
	// values mean "no fix" on iOS, and a zero/positive value is the 1σ
	// radius. Reject anything < 0; treat 0 as a perfect fix.
	if in.HorizontalAccuracyM < 0 {
		writeError(w, http.StatusBadRequest, "horizontal_accuracy_m must be ≥ 0")
		return
	}

	ctx := r.Context()

	var (
		state     string
		committed bool
		distance  float64
	)
	err := h.pool.QueryRow(ctx, `
		SELECT
			public.event_state(e) AS state,
			EXISTS(
				SELECT 1 FROM public.commits c
				WHERE c.event_id = e.id AND c.user_id = $1
			) AS committed,
			ST_Distance(
				e.geom::geography,
				ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography
			) AS distance_m
		FROM public.events e
		WHERE e.id = $4
	`, userID, in.Lon, in.Lat, eventID).Scan(&state, &committed, &distance)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select event: "+err.Error())
		return
	}

	if !committed {
		writeError(w, http.StatusForbidden, "not_attendee")
		return
	}
	if state != "Live" {
		writeError(w, http.StatusConflict, "not_live")
		return
	}

	if distance-in.HorizontalAccuracyM > geofenceFloorMeters {
		log.Printf("checkin event=%s user=%s outcome=reject_far distance_m=%.1f accuracy_m=%.1f",
			eventID, userID, distance, in.HorizontalAccuracyM)
		writeJSON(w, http.StatusConflict, notAtEventResponse{
			Error:     "not_at_event",
			DistanceM: distance,
			AccuracyM: in.HorizontalAccuracyM,
		})
		return
	}

	// ON CONFLICT DO UPDATE SET event_id = excluded.event_id is a no-op
	// that lets RETURNING surface the existing row's recorded_at — the
	// idempotent path returns the original timestamp, not now().
	var recordedAt time.Time
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO public.checkins (event_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (event_id, user_id) DO UPDATE SET event_id = excluded.event_id
		RETURNING recorded_at
	`, eventID, userID).Scan(&recordedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "insert checkin: "+err.Error())
		return
	}

	log.Printf("checkin event=%s user=%s outcome=accept distance_m=%.1f accuracy_m=%.1f",
		eventID, userID, distance, in.HorizontalAccuracyM)
	writeJSON(w, http.StatusOK, checkinResponse{RecordedAt: recordedAt})
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func parseEventID(r *http.Request) (string, bool) {
	raw := chi.URLParam(r, "id")
	if !uuidPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
