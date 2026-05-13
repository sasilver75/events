package chat_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/chat"
)

func TestStreamEndpoint(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	supabaseURL := os.Getenv("SUPABASE_URL")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	serviceKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if dbURL == "" || supabaseURL == "" || anonKey == "" || serviceKey == "" {
		t.Skip("DATABASE_URL, SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	// Cleanup order matters: ctx cancel must run BEFORE pool.Close so the
	// hub goroutine exits and releases its LISTEN connection. Cleanups run
	// LIFO, so pool.Close (registered first) runs *after* cancel
	// (registered second).
	t.Cleanup(pool.Close)
	t.Cleanup(cancel)

	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	hub := chat.NewHub(pool)
	hubErr := make(chan error, 1)
	go func() { hubErr <- hub.Run(ctx) }()

	stream := chat.NewStream(hub)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/events/{id}/messages/stream", stream.Handle)
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	hostID := seedHostID(ctx, t, pool)

	t.Run("non-attendee gets 403", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events/"+eventID+"/messages/stream", nil)
		req.Header.Set("Authorization", "Bearer "+tokenB)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("live delivery: POST after subscribe arrives within 2s", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		clientCtx, clientCancel := context.WithCancel(ctx)
		t.Cleanup(clientCancel)
		req, _ := http.NewRequestWithContext(clientCtx, http.MethodGet,
			srv.URL+"/events/"+eventID+"/messages/stream", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get stream: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream status: got %d", resp.StatusCode)
		}

		// Allow LISTEN subscription to register before inserting.
		time.Sleep(100 * time.Millisecond)
		_, err = chat.InsertSystemMessage(ctx, pool, eventID, "hello stream")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}

		br := bufio.NewReader(resp.Body)
		got := readSSEFrame(t, br, 2*time.Second)
		if got.Body != "hello stream" {
			t.Errorf("body: got %q, want hello stream", got.Body)
		}
	})

	t.Run("Last-Event-ID replays missed messages on reconnect", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		first, _ := chat.InsertSystemMessage(ctx, pool, eventID, "before")
		_, _ = chat.InsertSystemMessage(ctx, pool, eventID, "missed-1")
		_, _ = chat.InsertSystemMessage(ctx, pool, eventID, "missed-2")

		clientCtx, clientCancel := context.WithCancel(ctx)
		t.Cleanup(clientCancel)
		req, _ := http.NewRequestWithContext(clientCtx, http.MethodGet,
			srv.URL+"/events/"+eventID+"/messages/stream", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Last-Event-ID", fmt.Sprintf("%d", first.ID))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get stream: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream status: got %d", resp.StatusCode)
		}

		br := bufio.NewReader(resp.Body)
		got1 := readSSEFrame(t, br, 2*time.Second)
		got2 := readSSEFrame(t, br, 2*time.Second)
		if got1.Body != "missed-1" || got2.Body != "missed-2" {
			t.Errorf("replay: got %q,%q want missed-1,missed-2", got1.Body, got2.Body)
		}
	})
}

// readSSEFrame reads one full SSE frame (terminated by \n\n) from br and
// returns the data field decoded as a Message. Skips heartbeat comments
// (lines starting with `: `) and `id:` lines. The bufio.Reader must be
// reused across reads — a fresh wrapper around resp.Body would discard the
// bytes the previous reader buffered ahead.
func readSSEFrame(t *testing.T, br *bufio.Reader, timeout time.Duration) chat.Message {
	t.Helper()
	type result struct {
		m   chat.Message
		err error
	}
	out := make(chan result, 1)
	go func() {
		var dataBuf bytes.Buffer
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				out <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\n")
			if line == "" {
				if dataBuf.Len() == 0 {
					continue
				}
				var m chat.Message
				if err := json.Unmarshal(dataBuf.Bytes(), &m); err != nil {
					out <- result{err: fmt.Errorf("decode: %w", err)}
					return
				}
				out <- result{m: m}
				return
			}
			if strings.HasPrefix(line, ": ") {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	select {
	case r := <-out:
		if r.err != nil {
			t.Fatalf("read SSE frame: %v", r.err)
		}
		return r.m
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for SSE frame after %s", timeout)
		return chat.Message{}
	}
}
