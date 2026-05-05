package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
)

// Integration test: uses the local Supabase stack to mint a real JWT via the
// seeded test OTP path, then exercises the full middleware chain against /me.
// Also asserts the auth.users → public.users mirror trigger (migration 0006,
// ADR 0022) created the domain row inside Supabase Auth's transaction.
//
// Requires:
//   - `supabase start` running locally with [auth.sms.test_otp] enabling
//     phone "14152127777" → "123456" (the E.164 digits-only form GoTrue
//     derives from the iOS client's "+14152127777" input)
//   - DATABASE_URL pointing at the local Postgres
//   - SUPABASE_URL + SUPABASE_ANON_KEY set
//
// Skips when those env vars are missing so `go test ./...` is safe in CI
// environments without the stack.
func TestAuthMiddlewareEndToEnd(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	supabaseURL := os.Getenv("SUPABASE_URL")
	anonKey := os.Getenv("SUPABASE_ANON_KEY")
	if dbURL == "" || supabaseURL == "" || anonKey == "" {
		t.Skip("DATABASE_URL, SUPABASE_URL, SUPABASE_ANON_KEY required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/me", auth.Me)
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("garbage token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer not-a-real-jwt")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	// E.164 on the wire (`+14152127777`); GoTrue strips the `+` for both the
	// test_otp lookup and DB storage, so `dbPhone` is the digits-only form.
	const wirePhone = "+14152127777"
	const dbPhone = "14152127777"
	const testCode = "123456"

	// Force the trigger path: delete any prior auth.users row for the test
	// phone so the verify below creates a fresh user. The cascade clears
	// public.users with it.
	if _, err := pool.Exec(ctx, `DELETE FROM auth.users WHERE phone = $1`, dbPhone); err != nil {
		t.Fatalf("cleanup auth.users: %v", err)
	}

	signInWithTestOTP(t, supabaseURL, anonKey, wirePhone)
	token, userID := verifyTestOTP(t, supabaseURL, anonKey, wirePhone, testCode)

	t.Run("trigger mirrored auth.users to public.users at signup", func(t *testing.T) {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM public.users WHERE id = $1)`,
			userID,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if !exists {
			t.Errorf("public.users row missing for fresh auth.users %s — trigger did not fire", userID)
		}
	})

	t.Run("valid token returns user_id from /me", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var got map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got["user_id"] != userID {
			t.Errorf("user_id mismatch: want %s got %s", userID, got["user_id"])
		}
	})
}

func signInWithTestOTP(t *testing.T, supabaseURL, anonKey, phone string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"phone": phone})
	req, _ := http.NewRequest(http.MethodPost, supabaseURL+"/auth/v1/otp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("otp request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("otp send: HTTP %d: %s", resp.StatusCode, b)
	}
}

func verifyTestOTP(t *testing.T, supabaseURL, anonKey, phone, code string) (token, userID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"type":  "sms",
		"phone": phone,
		"token": code,
	})
	req, _ := http.NewRequest(http.MethodPost, supabaseURL+"/auth/v1/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("verify: HTTP %d: %s", resp.StatusCode, b)
	}
	var got struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	if got.AccessToken == "" || got.User.ID == "" {
		t.Fatalf("verify response missing access_token or user.id")
	}
	return got.AccessToken, got.User.ID
}
