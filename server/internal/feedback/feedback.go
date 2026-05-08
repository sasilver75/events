// Package feedback serves the post-event feedback flow (#35,
// PRD §Post-event + ADR 0008).
//
// GET  /events/{id}/feedback returns the list of fellow Show-outcome
//
//	Attendees the caller can rate, plus any
//	signals the caller has already submitted
//	(so reopen-and-edit works without a server
//	round-trip per row).
//
// POST /events/{id}/feedback writes a batch of {target, signal, reasons?}
//
//	tuples — feedback_signals always, flags only
//	when the 👎 carries at least one hard reason.
//	Idempotent on (event_id, voter_id, target_user_id):
//	re-submit overwrites both feedback_signals
//	and the corresponding flags row (or removes
//	it if the new signal is no longer a hard 👎).
//
// Window: open at Done, closes 24hr after end_time. Submissions after
// the window return 410 Gone.
//
// Reputation fan-out: every hard-flag delta (added or removed) recomputes
// the target's reputation row inside the same transaction, so a flagger
// sees their own score reflect their own cast (and the system is
// crash-safe — a half-applied flag without a recompute can't exist).
package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/reputation"
)

// feedbackWindow is the 24hr cut from PRD §Post-event — feedback
// submitted after end_time + this duration is rejected with 410 Gone.
const feedbackWindow = 24 * time.Hour

// Hard reasons (ADR 0008). A 👎 with at least one of these is a flag —
// it lands in public.flags and depresses the target's score via
// flag_factor. A 👎 with only 'just_didnt_like_them' is a soft 👎: it
// lives in feedback_signals for bundled-feedback display but doesn't
// move the dial.
var hardReasons = map[string]struct{}{
	"would_not_attend_with_again": {},
	"concerning_behavior":         {},
	"made_me_uncomfortable":       {},
}

const softReason = "just_didnt_like_them"

func validReason(r string) bool {
	if r == softReason {
		return true
	}
	_, ok := hardReasons[r]
	return ok
}

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type targetDTO struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type submittedDTO struct {
	TargetUserID string   `json:"target_user_id"`
	Signal       string   `json:"signal"`
	Reasons      []string `json:"reasons"`
}

type listResponse struct {
	WindowClosesAt time.Time      `json:"window_closes_at"`
	Targets        []targetDTO    `json:"targets"`
	Submitted      []submittedDTO `json:"submitted"`
}

type submitItem struct {
	TargetUserID string   `json:"target_user_id"`
	Signal       string   `json:"signal"`
	Reasons      []string `json:"reasons,omitempty"`
}

type submitRequest struct {
	Signals []submitItem `json:"signals"`
}

// List handles GET /events/{id}/feedback. The list itself is the same
// regardless of whether the caller has already submitted — the sheet
// always shows every fellow Show-outcome Attendee — and `submitted`
// carries any prior signals so the iOS UI can render the current state
// without a round-trip per row.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()

	endTime, callerOutcome, err := readGate(ctx, h.pool, eventID, userID)
	if err != nil {
		switch {
		case errors.Is(err, errNoEvent):
			writeError(w, http.StatusNotFound, "event not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if callerOutcome != "show" {
		writeError(w, http.StatusForbidden, "not_show_attendee")
		return
	}
	closesAt := endTime.Add(feedbackWindow)
	if time.Now().After(closesAt) {
		writeError(w, http.StatusGone, "feedback_window_closed")
		return
	}

	rows, err := h.pool.Query(ctx, `
		SELECT u.id, COALESCE(u.display_name, '')
		  FROM public.attendance_outcomes ao
		  JOIN public.users u ON u.id = ao.user_id
		 WHERE ao.event_id = $1
		   AND ao.outcome  = 'show'
		   AND ao.user_id <> $2
		 ORDER BY u.display_name NULLS LAST, u.id
	`, eventID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select targets: "+err.Error())
		return
	}
	defer rows.Close()
	targets := make([]targetDTO, 0)
	for rows.Next() {
		var t targetDTO
		if err := rows.Scan(&t.UserID, &t.DisplayName); err != nil {
			writeError(w, http.StatusInternalServerError, "scan target: "+err.Error())
			return
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows err: "+err.Error())
		return
	}

	subRows, err := h.pool.Query(ctx, `
		SELECT target_user_id, signal, reasons
		  FROM public.feedback_signals
		 WHERE event_id = $1 AND voter_id = $2
	`, eventID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select submitted: "+err.Error())
		return
	}
	defer subRows.Close()
	submitted := make([]submittedDTO, 0)
	for subRows.Next() {
		var s submittedDTO
		if err := subRows.Scan(&s.TargetUserID, &s.Signal, &s.Reasons); err != nil {
			writeError(w, http.StatusInternalServerError, "scan submitted: "+err.Error())
			return
		}
		submitted = append(submitted, s)
	}
	if err := subRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "submitted err: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, listResponse{
		WindowClosesAt: closesAt,
		Targets:        targets,
		Submitted:      submitted,
	})
}

// Submit handles POST /events/{id}/feedback. The whole batch lands in a
// single transaction — partial application would leave the score and
// the bundled-feedback display out of sync with the user's intent.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
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

	var in submitRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(in.Signals) == 0 {
		writeError(w, http.StatusBadRequest, "signals: at least one required")
		return
	}
	seen := make(map[string]struct{}, len(in.Signals))
	for i, s := range in.Signals {
		if !uuidPattern.MatchString(s.TargetUserID) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d].target_user_id: invalid uuid", i))
			return
		}
		if s.TargetUserID == userID {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d]: cannot rate self", i))
			return
		}
		if _, dup := seen[s.TargetUserID]; dup {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d]: duplicate target_user_id", i))
			return
		}
		seen[s.TargetUserID] = struct{}{}
		switch s.Signal {
		case "up", "down", "skip":
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d].signal: must be 'up' | 'down' | 'skip'", i))
			return
		}
		if s.Signal != "down" && len(s.Reasons) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d]: reasons only allowed on 'down'", i))
			return
		}
		for _, reason := range s.Reasons {
			if !validReason(reason) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("signals[%d]: unknown reason %q", i, reason))
				return
			}
		}
	}

	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	endTime, callerOutcome, err := readGate(ctx, tx, eventID, userID)
	if err != nil {
		switch {
		case errors.Is(err, errNoEvent):
			writeError(w, http.StatusNotFound, "event not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if callerOutcome != "show" {
		writeError(w, http.StatusForbidden, "not_show_attendee")
		return
	}
	if time.Now().After(endTime.Add(feedbackWindow)) {
		writeError(w, http.StatusGone, "feedback_window_closed")
		return
	}

	// Targets must be Committed Attendees (commits row present). Validating
	// against commits — not attendance_outcomes — keeps the rule aligned
	// with the issue spec: "Targets must be other Committed Attendees of
	// the event". A Show caller can flag a Ghost target (the iOS sheet
	// won't surface them, but a future surface might).
	for _, s := range in.Signals {
		var present bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM public.commits
				 WHERE event_id = $1 AND user_id = $2
			)
		`, eventID, s.TargetUserID).Scan(&present); err != nil {
			writeError(w, http.StatusInternalServerError, "validate target: "+err.Error())
			return
		}
		if !present {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("target %s is not a Committed Attendee of this event", s.TargetUserID))
			return
		}
	}

	recompute := make(map[string]struct{}, len(in.Signals))

	for _, s := range in.Signals {
		reasons := s.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.feedback_signals (event_id, voter_id, target_user_id, signal, reasons)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (event_id, voter_id, target_user_id)
			  DO UPDATE SET signal = EXCLUDED.signal,
			                reasons = EXCLUDED.reasons,
			                cast_at = now()
		`, eventID, userID, s.TargetUserID, s.Signal, reasons); err != nil {
			writeError(w, http.StatusInternalServerError, "upsert feedback_signals: "+err.Error())
			return
		}

		hardReasonsList := filterHard(reasons)
		isHardFlag := s.Signal == "down" && len(hardReasonsList) > 0

		// Was this triple previously a hard flag?
		var hadFlag bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM public.flags
				 WHERE event_id = $1 AND voter_id = $2 AND target_user_id = $3
			)
		`, eventID, userID, s.TargetUserID).Scan(&hadFlag); err != nil {
			writeError(w, http.StatusInternalServerError, "check flag: "+err.Error())
			return
		}

		if isHardFlag {
			// Hard reasons only — soft reason is captured in
			// feedback_signals.reasons but not propagated to flags.
			if _, err := tx.Exec(ctx, `
				INSERT INTO public.flags (event_id, voter_id, target_user_id, reasons)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (event_id, voter_id, target_user_id)
				  DO UPDATE SET reasons = EXCLUDED.reasons,
				                cast_at = now()
			`, eventID, userID, s.TargetUserID, hardReasonsList); err != nil {
				writeError(w, http.StatusInternalServerError, "upsert flag: "+err.Error())
				return
			}
			recompute[s.TargetUserID] = struct{}{}
		} else if hadFlag {
			if _, err := tx.Exec(ctx, `
				DELETE FROM public.flags
				 WHERE event_id = $1 AND voter_id = $2 AND target_user_id = $3
			`, eventID, userID, s.TargetUserID); err != nil {
				writeError(w, http.StatusInternalServerError, "delete flag: "+err.Error())
				return
			}
			recompute[s.TargetUserID] = struct{}{}
		}
	}

	for target := range recompute {
		if err := reputation.Recompute(ctx, tx, target); err != nil {
			writeError(w, http.StatusInternalServerError, "recompute reputation: "+err.Error())
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit tx: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"submitted": len(in.Signals)})
}

func filterHard(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if _, ok := hardReasons[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

var errNoEvent = errors.New("event not found")

// readGate reads the event's end_time and the caller's outcome on it.
// "" means no outcome row exists for the caller (treated as not_show
// at the call site). errNoEvent is the only typed error; everything
// else is wrapped for context.
func readGate(ctx context.Context, q queryRower, eventID, userID string) (endTime time.Time, callerOutcome string, err error) {
	row := q.QueryRow(ctx, `
		SELECT e.end_time,
		       COALESCE(
		         (SELECT outcome FROM public.attendance_outcomes
		           WHERE event_id = e.id AND user_id = $2),
		         ''
		       )
		  FROM public.events e
		 WHERE e.id = $1
	`, eventID, userID)
	if err := row.Scan(&endTime, &callerOutcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, "", errNoEvent
		}
		return time.Time{}, "", fmt.Errorf("read gate: %w", err)
	}
	return endTime, callerOutcome, nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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
