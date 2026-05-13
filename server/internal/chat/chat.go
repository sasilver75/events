// Package chat serves the per-Event chat endpoints (#65, PRD §At-event,
// ADR 0006). POST writes a user message, GET reads paginated history, and
// the SSE stream emits new messages over a short-lived per-screen connection.
//
// System messages (e.g. first-presence on check-in) are inserted by other
// packages via InsertSystemMessage — there is no exposed POST path for them.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

// maxBodyLen mirrors the schema CHECK in 0019_event_messages.up.sql. Kept
// in lock-step with the DB constraint so the handler returns a clean 400
// before Postgres rejects the row.
const maxBodyLen = 2000

// History pagination defaults. limitDefault matches a comfortable iOS chat
// initial render; limitMax caps replay storms when a long-disconnected SSE
// client reconnects with an ancient Last-Event-ID.
const (
	limitDefault = 100
	limitMax     = 500
)

type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type sendRequest struct {
	Body string `json:"body"`
}

// Message is the wire shape returned by Send and (in subsequent slices)
// the history/stream endpoints. ID is serialized as a JSON number — int64
// is safe for the BIGSERIAL sequence's lifetime well past v0 scale.
type Message struct {
	ID       int64     `json:"id"`
	EventID  string    `json:"event_id"`
	SenderID *string   `json:"sender_id"`
	Body     string    `json:"body"`
	SentAt   time.Time `json:"sent_at"`
	Kind     string    `json:"kind"`
}

// Send handles POST /events/{id}/messages.
//
// Order of checks matches checkins.CheckIn so the most actionable error
// surfaces first: existence → Committed-Attendee → chat-locked → body.
// β-Event chat is locked pre-Tip — α-Events and post-Tip β-Events are
// unlocked. Lock state is read from the same row that proves existence,
// so the read path is a single round-trip.
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
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

	var in sendRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	ctx := r.Context()

	var (
		tipThreshold *int16
		tippedAt     *time.Time
		committed    bool
	)
	err := h.pool.QueryRow(ctx, `
		SELECT
			e.tip_threshold,
			e.tipped_at,
			EXISTS(
				SELECT 1 FROM public.commits c
				WHERE c.event_id = e.id AND c.user_id = $1
			) AS committed
		FROM public.events e
		WHERE e.id = $2
	`, userID, eventID).Scan(&tipThreshold, &tippedAt, &committed)
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
	// β pre-Tip: chat is locked. α (tip_threshold IS NULL) and post-Tip β
	// (tipped_at IS NOT NULL) are unlocked.
	if tipThreshold != nil && tippedAt == nil {
		writeError(w, http.StatusConflict, "chat_locked")
		return
	}

	// Body validation runs after auth/state checks so a locked-room caller
	// gets "chat_locked" rather than a misleading "body too long".
	if len(in.Body) == 0 {
		writeError(w, http.StatusBadRequest, "body must not be empty")
		return
	}
	if len(in.Body) > maxBodyLen {
		writeError(w, http.StatusBadRequest, "body exceeds 2000 chars")
		return
	}

	var m Message
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO public.event_messages (event_id, sender_id, body, kind)
		VALUES ($1, $2, $3, 'user')
		RETURNING id, event_id, sender_id, body, sent_at, kind
	`, eventID, userID, in.Body).Scan(
		&m.ID, &m.EventID, &m.SenderID, &m.Body, &m.SentAt, &m.Kind,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert message: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// InsertSystemMessage is the entry point used by other packages (e.g.
// checkins, when first-presence fires the "Sam is here" message). Body is
// caller-supplied so the wording stays close to its source semantics.
func InsertSystemMessage(ctx context.Context, pool *pgxpool.Pool, eventID, body string) (Message, error) {
	var m Message
	err := pool.QueryRow(ctx, `
		INSERT INTO public.event_messages (event_id, sender_id, body, kind)
		VALUES ($1, NULL, $2, 'system')
		RETURNING id, event_id, sender_id, body, sent_at, kind
	`, eventID, body).Scan(
		&m.ID, &m.EventID, &m.SenderID, &m.Body, &m.SentAt, &m.Kind,
	)
	return m, err
}

// History handles GET /events/{id}/messages?since=<id>&limit=<n>.
//
// `since` is a monotonic message id (BIGSERIAL), not a timestamp — ADR 0006
// prescribes Last-Event-ID replay over the same cursor, so iOS uses the
// same param for first-fetch (since=0) and SSE-reconnect replay
// (since=<last received>). Default limit is 100; cap is 500 so a
// long-disconnected client can't ask for an unbounded replay.
//
// Auth path: existence + Committed-Attendee check via a single query that
// mirrors Send's, then a scoped fetch.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
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

	q := r.URL.Query()
	since := int64(0)
	if raw := q.Get("since"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "since must be a non-negative integer")
			return
		}
		since = v
	}
	limit := limitDefault
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if v > limitMax {
			v = limitMax
		}
		limit = v
	}

	ctx := r.Context()

	var (
		exists    bool
		committed bool
	)
	if err := h.pool.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM public.events WHERE id = $1) AS exists,
			EXISTS(
				SELECT 1 FROM public.commits c
				WHERE c.event_id = $1 AND c.user_id = $2
			) AS committed
	`, eventID, userID).Scan(&exists, &committed); err != nil {
		writeError(w, http.StatusInternalServerError, "select event: "+err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if !committed {
		writeError(w, http.StatusForbidden, "not_attendee")
		return
	}

	rows, err := h.pool.Query(ctx, `
		SELECT id, event_id, sender_id, body, sent_at, kind
		FROM public.event_messages
		WHERE event_id = $1 AND id > $2
		ORDER BY id ASC
		LIMIT $3
	`, eventID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select messages: "+err.Error())
		return
	}
	defer rows.Close()

	// Initialize non-nil so an empty page serializes as `[]` rather than `null`.
	out := make([]Message, 0, limit)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.EventID, &m.SenderID, &m.Body, &m.SentAt, &m.Kind); err != nil {
			writeError(w, http.StatusInternalServerError, "scan message: "+err.Error())
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "rows: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, out)
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
