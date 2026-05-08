package feedback_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/feedback"
)

const (
	testEmailA    = "feedback-test-a@spur.local"
	testPasswordA = "feedback-test-a-not-secret"
	testEmailB    = "feedback-test-b@spur.local"
	testPasswordB = "feedback-test-b-not-secret"
	testEmailC    = "feedback-test-c@spur.local"
	testPasswordC = "feedback-test-c-not-secret"
)

func TestFeedbackEndpoint(t *testing.T) {
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
	_ = ensureTestUser(t, supabaseURL, serviceKey, testEmailC, testPasswordC)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)
	userB := userIDFromToken(t, supabaseURL, serviceKey, testEmailB)
	userC := userIDFromToken(t, supabaseURL, serviceKey, testEmailC)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := feedback.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/events/{id}/feedback", h.List)
		r.Post("/events/{id}/feedback", h.Submit)
	})

	hostID := seedHostID(ctx, t, pool)

	// insertDoneEvent inserts an event already past end_time (two hours
	// ago); attendance_outcomes are written by the caller via insertOutcome
	// per fellow Attendee.
	insertDoneEvent := func(t *testing.T, endedAgo time.Duration) string {
		t.Helper()
		var id string
		// start_time is one hour before end_time so a 25hr-ago end_time
		// stays after a >25hr-ago start_time (events_end_after_start
		// CHECK is unforgiving).
		err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility
			) VALUES (
				$1, 'feedback test', 'feedback test', 'Other',
				now() - $2::interval - interval '1 hour', now() - $2::interval, 8,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public'
			) RETURNING id
		`, hostID, fmt.Sprintf("%d seconds", int(endedAgo.Seconds()))).Scan(&id)
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.flags WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.feedback_signals WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.attendance_outcomes WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.checkins WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.commits WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
		})
		return id
	}

	// resetUserState wipes per-test state for the long-lived test users
	// (their auth.users rows survive between tests). Reputation is a
	// derived cache so deleting it is safe; the next Recompute restores
	// it.
	resetUserState := func(t *testing.T, userIDs ...string) {
		t.Helper()
		for _, id := range userIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM public.reputation WHERE user_id = $1`, id)
		}
	}

	insertCommit := func(t *testing.T, eventID, userID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			eventID, userID,
		); err != nil {
			t.Fatalf("insert commit: %v", err)
		}
	}
	// upsert form because the lifecycle Runner in the same Go server
	// process may have already raced ahead and resolved a (possibly
	// different) outcome for this event. The test asserts the outcome it
	// installs, so DO UPDATE gives the test its desired ground truth
	// regardless of poller order.
	insertOutcome := func(t *testing.T, eventID, userID, outcome string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO public.attendance_outcomes (event_id, user_id, outcome)
			VALUES ($1, $2, $3)
			ON CONFLICT (event_id, user_id) DO UPDATE SET outcome = EXCLUDED.outcome
		`, eventID, userID, outcome); err != nil {
			t.Fatalf("insert outcome: %v", err)
		}
	}

	doPost := func(t *testing.T, eventID, token string, payload map[string]any) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/feedback", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	doGet := func(t *testing.T, eventID, token string) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/events/"+eventID+"/feedback", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/feedback", bytes.NewReader([]byte(`{"signals":[]}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid event id returns 400", func(t *testing.T) {
		rec, _ := doPost(t, "not-a-uuid", tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown event returns 404", func(t *testing.T) {
		rec, _ := doPost(t, "00000000-0000-0000-0000-000000000000", tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("Ghost caller cannot submit (403)", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "ghost")
		insertOutcome(t, eventID, userB, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("submission after window returns 410", func(t *testing.T) {
		eventID := insertDoneEvent(t, 25*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusGone {
			t.Fatalf("expected 410, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("self-rate rejected with 400", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertOutcome(t, eventID, userA, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userA, "signal": "up"}},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("non-attendee target rejected with 422", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertOutcome(t, eventID, userA, "show")
		// userB is not committed.

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("happy path: 👍 lands in feedback_signals only", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var sigCount, flagCount int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.feedback_signals WHERE event_id=$1 AND voter_id=$2 AND target_user_id=$3`,
			eventID, userA, userB,
		).Scan(&sigCount)
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.flags WHERE event_id=$1 AND voter_id=$2 AND target_user_id=$3`,
			eventID, userA, userB,
		).Scan(&flagCount)
		if sigCount != 1 {
			t.Errorf("feedback_signals: got %d, want 1", sigCount)
		}
		if flagCount != 0 {
			t.Errorf("flags: got %d, want 0", flagCount)
		}
	})

	t.Run("soft 👎 (just_didnt_like_them only) does NOT write a flag", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{
				"target_user_id": userB,
				"signal":         "down",
				"reasons":        []string{"just_didnt_like_them"},
			}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.flags WHERE event_id=$1 AND target_user_id=$2`,
			eventID, userB,
		).Scan(&n)
		if n != 0 {
			t.Errorf("flags: got %d, want 0", n)
		}
	})

	t.Run("hard 👎 writes a flag and recomputes target reputation", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")
		resetUserState(t, userA, userB, userC)

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{
				"target_user_id": userB,
				"signal":         "down",
				"reasons":        []string{"concerning_behavior"},
			}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var flagCount int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.flags WHERE event_id=$1 AND voter_id=$2 AND target_user_id=$3`,
			eventID, userA, userB,
		).Scan(&flagCount)
		if flagCount != 1 {
			t.Errorf("flags: got %d, want 1", flagCount)
		}
		var score int
		var present bool
		err := pool.QueryRow(ctx,
			`SELECT attendee_score FROM public.reputation WHERE user_id = $1`, userB,
		).Scan(&score)
		if err == nil {
			present = true
		}
		if !present {
			t.Errorf("expected reputation row for target after hard flag")
		}
	})

	t.Run("resubmit overwrites prior signal — hard 👎 → 👍 removes the flag", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		// First: hard flag.
		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{
				"target_user_id": userB,
				"signal":         "down",
				"reasons":        []string{"concerning_behavior"},
			}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("first submit: expected 200, got %d body=%s", rec.Code, body)
		}
		// Then: thumbs up — flag should disappear.
		rec, body = doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("second submit: expected 200, got %d body=%s", rec.Code, body)
		}
		var sig string
		_ = pool.QueryRow(ctx,
			`SELECT signal FROM public.feedback_signals WHERE event_id=$1 AND voter_id=$2 AND target_user_id=$3`,
			eventID, userA, userB,
		).Scan(&sig)
		if sig != "up" {
			t.Errorf("signal after overwrite: got %q, want 'up'", sig)
		}
		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.flags WHERE event_id=$1 AND voter_id=$2 AND target_user_id=$3`,
			eventID, userA, userB,
		).Scan(&n)
		if n != 0 {
			t.Errorf("flag should be removed after overwrite to 👍: got %d, want 0", n)
		}
	})

	t.Run("GET returns Show-outcome targets and prior submissions", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertCommit(t, eventID, userC)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")
		insertOutcome(t, eventID, userC, "ghost")

		// userA has already submitted on userB.
		_, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "up"}},
		})
		_ = body

		rec, raw := doGet(t, eventID, tokenA)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: expected 200, got %d body=%s", rec.Code, raw)
		}
		var resp struct {
			Targets   []map[string]any `json:"targets"`
			Submitted []map[string]any `json:"submitted"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v: %s", err, raw)
		}
		// Only Show-outcome non-self attendees should be returned. userC is
		// Ghost, userA is self.
		if len(resp.Targets) != 1 {
			t.Errorf("targets: got %d, want 1 (just userB)", len(resp.Targets))
		}
		if len(resp.Submitted) != 1 {
			t.Errorf("submitted: got %d, want 1", len(resp.Submitted))
		}
	})

	t.Run("validation: 'down' with no reasons is allowed (counts as soft 👎 but writes no flag)", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{"target_user_id": userB, "signal": "down"}},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.flags WHERE event_id=$1 AND target_user_id=$2`,
			eventID, userB,
		).Scan(&n)
		if n != 0 {
			t.Errorf("flag count: got %d, want 0", n)
		}
	})

	t.Run("validation: reasons not allowed on 'up' returns 400", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, _ := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{
				"target_user_id": userB,
				"signal":         "up",
				"reasons":        []string{"concerning_behavior"},
			}},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("validation: unknown reason returns 400", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, _ := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{{
				"target_user_id": userB,
				"signal":         "down",
				"reasons":        []string{"i_just_dont_vibe_with_them"},
			}},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("batch submission of multiple signals lands transactionally", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertCommit(t, eventID, userC)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")
		insertOutcome(t, eventID, userC, "show")

		rec, body := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{
				{"target_user_id": userB, "signal": "up"},
				{"target_user_id": userC, "signal": "skip"},
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.feedback_signals WHERE event_id=$1 AND voter_id=$2`,
			eventID, userA,
		).Scan(&n)
		if n != 2 {
			t.Errorf("feedback_signals: got %d, want 2", n)
		}
	})

	t.Run("validation: duplicate target in batch returns 400", func(t *testing.T) {
		eventID := insertDoneEvent(t, 1*time.Hour)
		insertCommit(t, eventID, userA)
		insertCommit(t, eventID, userB)
		insertOutcome(t, eventID, userA, "show")
		insertOutcome(t, eventID, userB, "show")

		rec, _ := doPost(t, eventID, tokenA, map[string]any{
			"signals": []map[string]any{
				{"target_user_id": userB, "signal": "up"},
				{"target_user_id": userB, "signal": "skip"},
			},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	_ = tokenB
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
