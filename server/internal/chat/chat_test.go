package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const (
	testEmailA    = "chat-test-a@spur.local"
	testPasswordA = "chat-test-a-not-secret"
	testEmailB    = "chat-test-b@spur.local"
	testPasswordB = "chat-test-b-not-secret"
)

type sendRequest struct {
	Body string `json:"body"`
}

type errorBody struct {
	Error string `json:"error"`
}

func TestSendMessageEndpoint(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	supabaseURL := os.Getenv("SUPABASE_URL")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	serviceKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if dbURL == "" || supabaseURL == "" || anonKey == "" || serviceKey == "" {
		t.Skip("DATABASE_URL, SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)
	userB := userIDFromToken(t, supabaseURL, serviceKey, testEmailB)
	_ = userB

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := chat.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/events/{id}/messages", h.Send)
	})

	hostID := seedHostID(ctx, t, pool)

	post := func(t *testing.T, eventID, token, body string) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		raw, _ := json.Marshal(sendRequest{Body: body})
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/messages", bytes.NewReader(raw))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		rec, _ := post(t, eventID, "", "hi")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid event id returns 400", func(t *testing.T) {
		rec, _ := post(t, "not-a-uuid", tokenA, "hi")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown event returns 404", func(t *testing.T) {
		const bogus = "00000000-0000-0000-0000-000000000000"
		rec, _ := post(t, bogus, tokenA, "hi")
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("non-attendee returns 403 not_attendee", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		rec, body := post(t, eventID, tokenA, "hi")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
		if e := decodeError(t, body); e.Error != "not_attendee" {
			t.Errorf("error: got %q, want %q", e.Error, "not_attendee")
		}
	})

	t.Run("β pre-Tip rejects with 409 chat_locked", func(t *testing.T) {
		eventID := insertBetaEvent(ctx, t, pool, hostID, 4, 3)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		rec, body := post(t, eventID, tokenA, "hi")
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if e := decodeError(t, body); e.Error != "chat_locked" {
			t.Errorf("error: got %q, want %q", e.Error, "chat_locked")
		}
	})

	t.Run("β post-Tip from attendee succeeds with 201", func(t *testing.T) {
		eventID := insertBetaEvent(ctx, t, pool, hostID, 4, 2)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		setTipped(ctx, t, pool, eventID)
		rec, body := post(t, eventID, tokenA, "hello")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, body)
		}
		var m chat.Message
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		if m.Kind != "user" {
			t.Errorf("kind: got %q, want user", m.Kind)
		}
		if m.SenderID == nil || *m.SenderID != userA {
			t.Errorf("sender_id: got %v, want %s", m.SenderID, userA)
		}
		if m.Body != "hello" {
			t.Errorf("body: got %q, want hello", m.Body)
		}
		if m.ID == 0 {
			t.Error("id is zero")
		}
		if m.SentAt.IsZero() {
			t.Error("sent_at is zero")
		}
	})

	t.Run("α from attendee succeeds at creation time (no Tip required)", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		rec, body := post(t, eventID, tokenA, "hi")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("empty body returns 400", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		rec, _ := post(t, eventID, tokenA, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("body over 2000 chars returns 400", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		rec, _ := post(t, eventID, tokenA, strings.Repeat("x", 2001))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("message row persists in DB", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, body := post(t, eventID, tokenA, "persisted?")
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, body)
		}
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.event_messages WHERE event_id = $1 AND body = 'persisted?'`,
			eventID,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Errorf("row count: got %d, want 1", n)
		}
	})
}

func TestHistoryEndpoint(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	supabaseURL := os.Getenv("SUPABASE_URL")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	serviceKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if dbURL == "" || supabaseURL == "" || anonKey == "" || serviceKey == "" {
		t.Skip("DATABASE_URL, SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	t.Cleanup(pool.Close)

	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := chat.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/events/{id}/messages", h.History)
	})

	hostID := seedHostID(ctx, t, pool)

	get := func(t *testing.T, eventID, token, query string) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		url := "/events/" + eventID + "/messages"
		if query != "" {
			url += "?" + query
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		rec, _ := get(t, eventID, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid event id returns 400", func(t *testing.T) {
		rec, _ := get(t, "not-a-uuid", tokenA, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown event returns 404", func(t *testing.T) {
		const bogus = "00000000-0000-0000-0000-000000000000"
		rec, _ := get(t, bogus, tokenA, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("non-attendee returns 403 not_attendee", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		rec, body := get(t, eventID, tokenB, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
		if e := decodeError(t, body); e.Error != "not_attendee" {
			t.Errorf("error: got %q, want not_attendee", e.Error)
		}
	})

	t.Run("happy path: returns messages ordered by id ascending", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		_, _ = chat.InsertSystemMessage(ctx, pool, eventID, "first")
		insertUserMessage(ctx, t, pool, eventID, userA, "second")
		_, _ = chat.InsertSystemMessage(ctx, pool, eventID, "third")

		rec, body := get(t, eventID, tokenA, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var msgs []chat.Message
		if err := json.Unmarshal(body, &msgs); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		if len(msgs) != 3 {
			t.Fatalf("len: got %d, want 3", len(msgs))
		}
		if msgs[0].Body != "first" || msgs[1].Body != "second" || msgs[2].Body != "third" {
			t.Errorf("order: got %q,%q,%q", msgs[0].Body, msgs[1].Body, msgs[2].Body)
		}
		// Strict id-monotonic — the schema guarantees this; we verify the
		// handler doesn't disturb it.
		if msgs[0].ID >= msgs[1].ID || msgs[1].ID >= msgs[2].ID {
			t.Errorf("ids not monotonic: %d, %d, %d", msgs[0].ID, msgs[1].ID, msgs[2].ID)
		}
	})

	t.Run("since=X returns only messages with id > X", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		first, _ := chat.InsertSystemMessage(ctx, pool, eventID, "first")
		insertUserMessage(ctx, t, pool, eventID, userA, "second")
		insertUserMessage(ctx, t, pool, eventID, userA, "third")

		rec, body := get(t, eventID, tokenA, fmt.Sprintf("since=%d", first.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var msgs []chat.Message
		_ = json.Unmarshal(body, &msgs)
		if len(msgs) != 2 {
			t.Fatalf("len: got %d, want 2 (after first)", len(msgs))
		}
		if msgs[0].Body != "second" || msgs[1].Body != "third" {
			t.Errorf("got %q,%q", msgs[0].Body, msgs[1].Body)
		}
	})

	t.Run("limit caps page size", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		for i := 0; i < 5; i++ {
			insertUserMessage(ctx, t, pool, eventID, userA, fmt.Sprintf("m%d", i))
		}
		rec, body := get(t, eventID, tokenA, "limit=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var msgs []chat.Message
		_ = json.Unmarshal(body, &msgs)
		if len(msgs) != 2 {
			t.Errorf("len: got %d, want 2", len(msgs))
		}
	})

	t.Run("empty page serializes as [] not null", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, body := get(t, eventID, tokenA, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if string(bytes.TrimSpace(body)) != "[]" {
			t.Errorf("body: got %q, want []", body)
		}
	})

	t.Run("non-numeric since returns 400", func(t *testing.T) {
		eventID := insertAlphaEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)
		rec, _ := get(t, eventID, tokenA, "since=oops")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})
}

// insertUserMessage is a server-side shortcut for seeding messages without
// running them through the POST endpoint — useful when the test focuses on
// the read path.
func insertUserMessage(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID, userID, body string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.event_messages (event_id, sender_id, body, kind)
		 VALUES ($1, $2, $3, 'user')`,
		eventID, userID, body,
	); err != nil {
		t.Fatalf("insert user message: %v", err)
	}
}

func TestInsertSystemMessage(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	// t.Cleanup so the event-row delete (also registered via t.Cleanup
	// below) still has a live pool when it runs. A bare `defer pool.Close()`
	// would close the pool before any t.Cleanup callbacks fire.
	t.Cleanup(pool.Close)

	hostID := seedHostID(ctx, t, pool)
	eventID := insertAlphaEvent(ctx, t, pool, hostID)
	t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })

	m, err := chat.InsertSystemMessage(ctx, pool, eventID, "Sam is here")
	if err != nil {
		t.Fatalf("InsertSystemMessage: %v", err)
	}
	if m.Kind != "system" {
		t.Errorf("kind: got %q, want system", m.Kind)
	}
	if m.SenderID != nil {
		t.Errorf("sender_id: got %v, want nil for system kind", m.SenderID)
	}
	if m.Body != "Sam is here" {
		t.Errorf("body: got %q", m.Body)
	}
}

// ---- helpers (copies of patterns in checkins_test.go / commits_test.go) ----

func decodeError(t *testing.T, body []byte) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error: %v: %s", err, body)
	}
	return e
}

func seedHostID(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&id); err != nil {
		t.Fatalf("find spur-seed host: %v", err)
	}
	return id
}

func insertAlphaEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'chat α', 'chat α', 'Other',
			now() + interval '1 hour', now() + interval '2 hours', 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID).Scan(&id)
	if err != nil {
		t.Fatalf("insert α event: %v", err)
	}
	return id
}

func insertBetaEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID string, cap, threshold int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility,
			tip_threshold, tip_deadline
		) VALUES (
			$1, 'chat β', 'chat β', 'Other',
			now() + interval '2 hours', now() + interval '3 hours', $2,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public',
			$3, now() + interval '1 hour'
		) RETURNING id
	`, hostID, cap, threshold).Scan(&id)
	if err != nil {
		t.Fatalf("insert β event: %v", err)
	}
	return id
}

func setTipped(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE public.events SET tipped_at = now() WHERE id = $1`, eventID,
	); err != nil {
		t.Fatalf("set tipped_at: %v", err)
	}
}

func deleteEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id); err != nil {
		t.Errorf("delete event: %v", err)
	}
}

func insertCommit(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID, userID string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		eventID, userID,
	); err != nil {
		t.Fatalf("insert commit: %v", err)
	}
}

func ensureTestUser(t *testing.T, supabaseURL, serviceKey, email, password string) string {
	t.Helper()
	if id, found := adminFindUserByEmail(t, supabaseURL, serviceKey, email); found {
		return id
	}
	body, _ := json.Marshal(map[string]any{
		"email":         email,
		"password":      password,
		"email_confirm": true,
	})
	req, _ := http.NewRequest(http.MethodPost, supabaseURL+"/auth/v1/admin/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin create user: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create user: HTTP %d: %s", resp.StatusCode, respBody)
	}
	var u struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &u); err != nil {
		t.Fatalf("decode created user: %v", err)
	}
	return u.ID
}

func adminFindUserByEmail(t *testing.T, supabaseURL, serviceKey, email string) (string, bool) {
	t.Helper()
	url := fmt.Sprintf("%s/auth/v1/admin/users?email=%s", supabaseURL, email)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin list users: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list users: HTTP %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode users list: %v", err)
	}
	for _, u := range list.Users {
		if u.Email == email {
			return u.ID, true
		}
	}
	return "", false
}

func userIDFromToken(t *testing.T, supabaseURL, serviceKey, email string) string {
	t.Helper()
	id, ok := adminFindUserByEmail(t, supabaseURL, serviceKey, email)
	if !ok {
		t.Fatalf("user not found for %s", email)
	}
	return id
}

func signInWithPassword(t *testing.T, supabaseURL, anonKey, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost, supabaseURL+"/auth/v1/token?grant_type=password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("password sign-in: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password sign-in: HTTP %d: %s", resp.StatusCode, respBody)
	}
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(respBody, &got); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	_ = time.Now()
	return got.AccessToken
}
