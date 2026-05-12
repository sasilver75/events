package commits

import (
	"net/http"
	"time"

	"github.com/sasilver75/events/server/internal/auth"
)

// recentWindow is how far back a Done Event remains in the "Recent" section.
// Trades discoverability of last week's stuff against feed density — 7 days
// keeps the surface bounded without pagination at v0 scales (PRD US 46).
const recentWindow = 7 * 24 * time.Hour

type myCommitEvent struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Category  string    `json:"category"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	State     string    `json:"state"`
}

type myCommitsResponse struct {
	Upcoming []myCommitEvent `json:"upcoming"`
	Recent   []myCommitEvent `json:"recent"`
}

// MyCommits handles GET /users/me/commits. Returns the caller's active and
// recent Commits with their parent Event projection so the iOS "Your Events"
// tab can render upcoming and post-event rows without further round trips.
//
// Windows:
//   - upcoming: parent Event's end_time > now() (live Events stay until end).
//   - recent:   parent Event's end_time in (now() - recentWindow, now()].
//
// Withdrawn Commits are naturally excluded — Withdraw deletes the commits row
// so the join below has nothing to project. Cancelled Events flow through as
// Cancelled in the state column (the Commit row persists) so the Attendee can
// see what they were Committed to even after a Host cancel.
//
// Coordinates: the visibility CASE mirrors events.Near (public → exact, fuzzed
// + Committed → exact, fuzzed + non-Committed → display_geom). The caller is
// by definition Committed to every row in this projection, so the result is
// always exact — but we keep the explicit branch to match the existing
// projection helper, not invent a parallel shape (see PRD §Geo data model).
func (h *Handler) MyCommits(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no user in context")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT
			e.id,
			e.title,
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
			public.event_state(e) AS state,
			(e.end_time > now()) AS upcoming
		FROM public.commits c
		JOIN public.events e ON e.id = c.event_id
		WHERE c.user_id = $1
			AND e.end_time > $2
		ORDER BY e.start_time ASC
	`, userID, time.Now().Add(-recentWindow))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query my commits: "+err.Error())
		return
	}
	defer rows.Close()

	out := myCommitsResponse{
		Upcoming: []myCommitEvent{},
		Recent:   []myCommitEvent{},
	}
	for rows.Next() {
		var e myCommitEvent
		var upcoming bool
		if err := rows.Scan(
			&e.ID, &e.Title, &e.Category, &e.StartTime, &e.EndTime,
			&e.Lat, &e.Lon, &e.State, &upcoming,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan: "+err.Error())
			return
		}
		if upcoming {
			out.Upcoming = append(out.Upcoming, e)
		} else {
			out.Recent = append(out.Recent, e)
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, out)
}
