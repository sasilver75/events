// Package events serves the Browse-flow read endpoints. Writes (create,
// commit, withdraw, …) live elsewhere — this package is intentionally narrow.
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

const defaultRadiusM = 10000

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
	StartAt     time.Time `json:"start_at"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	Cap         int       `json:"cap"`
	CommitCount int       `json:"commit_count"`
}

// Near handles GET /events?near=lat,lon&radius_m=…
//
// Coordinate-visibility branches per PRD-v0:290 / CONTEXT.md §Location fuzzing:
//   - location_visibility='public'  → exact center for all viewers
//   - location_visibility='fuzzed' + viewer NOT Committed → display_geom
//   - location_visibility='fuzzed' + viewer Committed     → exact center
//
// Past Events (start_at + duration_minutes ≤ now) are excluded server-side.
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

	// Geographic filter is on the *true* center, not display_geom: a viewer
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
			e.start_at,
			CASE
				WHEN e.location_visibility = 'public' OR c.user_id IS NOT NULL
					THEN e.center_lat
				ELSE ST_Y(e.display_geom)
			END AS lat,
			CASE
				WHEN e.location_visibility = 'public' OR c.user_id IS NOT NULL
					THEN e.center_lon
				ELSE ST_X(e.display_geom)
			END AS lon,
			e.cap,
			(SELECT count(*) FROM public.commits cc WHERE cc.event_id = e.id) AS commit_count
		FROM public.events e
		LEFT JOIN public.commits c
			ON c.event_id = e.id AND c.user_id = $3
		WHERE
			ST_DWithin(
				ST_SetSRID(ST_MakePoint(e.center_lon, e.center_lat), 4326)::geography,
				ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
				$4
			)
			AND e.start_at + make_interval(mins => e.duration_minutes) > now()
		ORDER BY e.start_at ASC
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
			&e.ID, &e.Title, &e.Description, &e.Category, &e.StartAt,
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
