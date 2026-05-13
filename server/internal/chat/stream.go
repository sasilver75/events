package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

// fanoutBuffer is the per-subscriber channel buffer. Each notification
// payload is a single int64, so the cost is trivial; the buffer absorbs
// transient writer slowness without dropping. On overflow we drop the
// notification rather than block the pump — the SSE client will pick up
// missed ids on its next Last-Event-ID reconnect.
const fanoutBuffer = 64

// sseHeartbeat is the keepalive interval. iOS NSURLSession and most
// intermediate proxies idle-close streams after ~60s without bytes; a
// comment frame every 25s keeps the connection warm without spending a
// real message id.
const sseHeartbeat = 25 * time.Second

// Hub owns the single LISTEN connection and the per-event subscriber map.
// Subscribers receive newly-inserted message ids; the SSE handler hydrates
// the row by id before writing the frame.
type Hub struct {
	pool *pgxpool.Pool

	mu   sync.Mutex
	subs map[string]map[chan int64]struct{}
}

func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{
		pool: pool,
		subs: make(map[string]map[chan int64]struct{}),
	}
}

// Run pumps NOTIFY chat_message payloads into per-event subscriber
// channels. Blocks until ctx is cancelled. Acquires a dedicated connection
// from the pool — LISTEN holds it for the loop's lifetime, so the rest of
// the pool stays free for normal queries.
//
// On connection loss the loop reconnects with exponential backoff; the
// pgxpool.Conn release path returns the (now-broken) connection so the
// pool reissues a fresh one.
func (h *Hub) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		if err := h.runOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("chat hub: listen loop exited: %v; reconnecting in %s", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return nil
	}
}

func (h *Hub) runOnce(ctx context.Context) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN chat_message"); err != nil {
		return fmt.Errorf("LISTEN: %w", err)
	}

	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		var payload struct {
			EventID   string `json:"event_id"`
			MessageID int64  `json:"message_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
			log.Printf("chat hub: bad NOTIFY payload %q: %v", n.Payload, err)
			continue
		}
		h.fanout(payload.EventID, payload.MessageID)
	}
}

func (h *Hub) fanout(eventID string, messageID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[eventID] {
		// Non-blocking send: a slow subscriber loses notifications but
		// doesn't stall the pump. The dropped ids are recoverable via
		// Last-Event-ID on reconnect.
		select {
		case ch <- messageID:
		default:
		}
	}
}

func (h *Hub) subscribe(eventID string) chan int64 {
	ch := make(chan int64, fanoutBuffer)
	h.mu.Lock()
	if _, ok := h.subs[eventID]; !ok {
		h.subs[eventID] = make(map[chan int64]struct{})
	}
	h.subs[eventID][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(eventID string, ch chan int64) {
	h.mu.Lock()
	if subs, ok := h.subs[eventID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.subs, eventID)
		}
	}
	h.mu.Unlock()
	close(ch)
}

// Stream handles GET /events/{id}/messages/stream.
//
// Lifecycle:
//  1. Auth + Committed-Attendee gate (mirrors History/Send).
//  2. If Last-Event-ID is set, replay rows with id > that cursor in the
//     same connection before going live. The replay query runs *after*
//     subscribing, so any message inserted between the two reads is still
//     delivered via the live channel — id-monotonicity dedupes the overlap
//     on the client.
//  3. Live loop: select on the subscriber channel + heartbeat ticker +
//     request context. On context cancel (client disconnect, server
//     shutdown) unsubscribe and return.
//
// SSE wire format: each frame is
//
//	id: <message_id>
//	data: <JSON Message>
//	\n
//
// The `id:` line is what populates the client's Last-Event-ID on reconnect.
// Heartbeats use the `: ` comment form which carries no id.
func (s *Stream) Handle(w http.ResponseWriter, r *http.Request) {
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()
	pool := s.hub.pool

	var (
		exists    bool
		committed bool
	)
	if err := pool.QueryRow(ctx, `
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.hub.subscribe(eventID)
	defer s.hub.unsubscribe(eventID, ch)

	if cursor := parseLastEventID(r); cursor > 0 {
		if err := s.replay(ctx, w, flusher, eventID, cursor); err != nil {
			log.Printf("chat stream: replay event=%s err=%v", eventID, err)
			return
		}
	}

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case id, ok := <-ch:
			if !ok {
				return
			}
			m, err := s.fetchMessage(ctx, eventID, id)
			if err != nil {
				log.Printf("chat stream: fetch event=%s id=%d err=%v", eventID, id, err)
				continue
			}
			if err := writeSSEFrame(w, m); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Stream) replay(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, eventID string, cursor int64) error {
	rows, err := s.hub.pool.Query(ctx, `
		SELECT id, event_id, sender_id, body, sent_at, kind
		FROM public.event_messages
		WHERE event_id = $1 AND id > $2
		ORDER BY id ASC
		LIMIT $3
	`, eventID, cursor, limitMax)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.EventID, &m.SenderID, &m.Body, &m.SentAt, &m.Kind); err != nil {
			return err
		}
		if err := writeSSEFrame(w, m); err != nil {
			return err
		}
	}
	flusher.Flush()
	return rows.Err()
}

func (s *Stream) fetchMessage(ctx context.Context, eventID string, id int64) (Message, error) {
	var m Message
	err := s.hub.pool.QueryRow(ctx, `
		SELECT id, event_id, sender_id, body, sent_at, kind
		FROM public.event_messages
		WHERE event_id = $1 AND id = $2
	`, eventID, id).Scan(&m.ID, &m.EventID, &m.SenderID, &m.Body, &m.SentAt, &m.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, fmt.Errorf("message %d on event %s not found", id, eventID)
	}
	return m, err
}

func writeSSEFrame(w http.ResponseWriter, m Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", m.ID, body); err != nil {
		return err
	}
	return nil
}

func parseLastEventID(r *http.Request) int64 {
	// Browser EventSource sends `Last-Event-ID`; some non-browser clients
	// (and curl) prefer a query param. Accept both. Garbage values fall
	// through to 0 (start of stream) — the client gets a full replay,
	// which is the safe default.
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
			return v
		}
	}
	if raw := r.URL.Query().Get("last_event_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
			return v
		}
	}
	return 0
}

// Stream is the SSE-specific handler — kept separate from Handler so its
// hub dependency is explicit and Send/History can be served without
// constructing the hub (useful for tests that only need the request path).
type Stream struct {
	hub *Hub
}

func NewStream(hub *Hub) *Stream {
	return &Stream{hub: hub}
}
