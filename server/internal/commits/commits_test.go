package commits_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/commits"
)

const (
	testEmailA    = "commits-test-a@spur.local"
	testPasswordA = "commits-test-a-not-secret"
	testEmailB    = "commits-test-b@spur.local"
	testPasswordB = "commits-test-b-not-secret"
)

type commitResponse struct {
	CommitCount   int  `json:"commit_count"`
	CommittedByMe bool `json:"committed_by_me"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func TestCommitsEndpoints(t *testing.T) {
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

	userA := ensureTestUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	userB := ensureTestUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := commits.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/events/{id}/commit", h.Commit)
		r.Delete("/events/{id}/commit", h.Withdraw)
	})

	hostID := seedHostID(ctx, t, pool)

	clearCommits := func(t *testing.T, eventID string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `DELETE FROM public.commits WHERE event_id = $1`, eventID); err != nil {
			t.Fatalf("clear commits: %v", err)
		}
	}

	doRequest := func(t *testing.T, method, path, token string) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec, body
	}

	decodeOK := func(t *testing.T, body []byte) commitResponse {
		t.Helper()
		var got commitResponse
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode response: %v: %s", err, body)
		}
		return got
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 4)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })

		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/commit", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("commit on unknown event returns 404", func(t *testing.T) {
		const bogus = "00000000-0000-0000-0000-000000000000"
		rec, _ := doRequest(t, http.MethodPost, "/events/"+bogus+"/commit", tokenA)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("commit happy path returns 200 with count=1 and committed_by_me=true", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 4)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		rec, body := doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		got := decodeOK(t, body)
		if got.CommitCount != 1 {
			t.Errorf("commit_count: got %d, want 1", got.CommitCount)
		}
		if !got.CommittedByMe {
			t.Errorf("committed_by_me: got false, want true")
		}

		var dbCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.commits WHERE event_id = $1`, eventID,
		).Scan(&dbCount); err != nil {
			t.Fatalf("count commits: %v", err)
		}
		if dbCount != 1 {
			t.Errorf("DB row count: got %d, want 1", dbCount)
		}
	})

	t.Run("repeat commit by same user is idempotent", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 4)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		_, _ = doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenA)
		rec, body := doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		got := decodeOK(t, body)
		if got.CommitCount != 1 {
			t.Errorf("commit_count: got %d, want 1 (idempotent)", got.CommitCount)
		}
		if !got.CommittedByMe {
			t.Errorf("committed_by_me: got false, want true")
		}
	})

	t.Run("withdraw after commit returns 200 with count=0 and committed_by_me=false", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 4)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		_, _ = doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenA)
		rec, body := doRequest(t, http.MethodDelete, "/events/"+eventID+"/commit", tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		got := decodeOK(t, body)
		if got.CommitCount != 0 {
			t.Errorf("commit_count: got %d, want 0", got.CommitCount)
		}
		if got.CommittedByMe {
			t.Errorf("committed_by_me: got true, want false")
		}
	})

	t.Run("repeat withdraw is idempotent", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 4)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		rec, body := doRequest(t, http.MethodDelete, "/events/"+eventID+"/commit", tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		got := decodeOK(t, body)
		if got.CommitCount != 0 || got.CommittedByMe {
			t.Errorf("got %+v, want {0 false}", got)
		}
	})

	t.Run("commit on full event returns 409 event_full", func(t *testing.T) {
		eventID := insertEvent(ctx, t, pool, hostID, 1)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		rec, body := doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("user A first commit: expected 200, got %d body=%s", rec.Code, body)
		}

		rec, body = doRequest(t, http.MethodPost, "/events/"+eventID+"/commit", tokenB)
		if rec.Code != http.StatusConflict {
			t.Fatalf("user B at cap: expected 409, got %d body=%s", rec.Code, body)
		}
		var er errorResponse
		if err := json.Unmarshal(body, &er); err != nil {
			t.Fatalf("decode error: %v: %s", err, body)
		}
		if er.Error != "event_full" {
			t.Errorf("error: got %q, want %q", er.Error, "event_full")
		}
	})

	// Concurrency assertion: two simultaneous Commits at cap-1 must produce
	// exactly one success and one 409. Uses a real httptest.Server so each
	// request flows over its own connection and the Postgres lock actually
	// contends — r.ServeHTTP serialises through a single goroutine and would
	// hide the race.
	t.Run("concurrent commits at cap=1: exactly one wins", func(t *testing.T) {
		_ = userA
		_ = userB
		eventID := insertEvent(ctx, t, pool, hostID, 1)
		t.Cleanup(func() { clearCommits(t, eventID); deleteEvent(ctx, t, pool, eventID) })

		srv := httptest.NewServer(r)
		t.Cleanup(srv.Close)

		const racers = 2
		var ok, conflict int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		tokens := []string{tokenA, tokenB}
		codes := make([]int, racers)
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodPost, srv.URL+"/events/"+eventID+"/commit", nil)
				req.Header.Set("Authorization", "Bearer "+tokens[i])
				<-start
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Errorf("racer %d: %v", i, err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				codes[i] = resp.StatusCode
				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt32(&ok, 1)
				case http.StatusConflict:
					atomic.AddInt32(&conflict, 1)
				}
			}(i)
		}
		close(start)
		wg.Wait()

		if ok != 1 || conflict != 1 {
			t.Fatalf("expected 1×200 + 1×409, got %d×200 + %d×409 (codes=%v)", ok, conflict, codes)
		}

		var dbCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.commits WHERE event_id = $1`, eventID,
		).Scan(&dbCount); err != nil {
			t.Fatalf("count commits: %v", err)
		}
		if dbCount != 1 {
			t.Errorf("DB row count: got %d, want 1", dbCount)
		}
	})
}

func seedHostID(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var hostID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&hostID); err != nil {
		t.Fatalf("find spur-seed host: %v", err)
	}
	return hostID
}

func insertEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID string, cap int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'commits test', 'commits test', 'Other',
			now() + interval '1 hour', now() + interval '2 hours', $2,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID, cap).Scan(&id)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func deleteEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id); err != nil {
		t.Errorf("delete event: %v", err)
	}
}

// ensureTestUser idempotently creates a Supabase user via Admin API and
// returns its UUID. Duplicated from internal/events test harness; an
// extraction is warranted once a third call site appears.
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
	if u.ID == "" {
		t.Fatalf("admin create user: empty id: %s", respBody)
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

func signInWithPassword(t *testing.T, supabaseURL, anonKey, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
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
	if got.AccessToken == "" {
		t.Fatalf("missing access_token: %s", respBody)
	}
	return got.AccessToken
}
