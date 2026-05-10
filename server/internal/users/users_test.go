package users_test

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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/users"
)

const (
	testEmailA    = "users-test-a@spur.local"
	testPasswordA = "users-test-a-not-secret"
	testEmailB    = "users-test-b@spur.local"
	testPasswordB = "users-test-b-not-secret"
	testEmailC    = "users-test-c@spur.local"
	testPasswordC = "users-test-c-not-secret"

	tosCurrentVersion = "v1"
)

func TestUsersEndpoints(t *testing.T) {
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

	// Three fresh test identities. Each subtest deletes the public.users
	// row for its caller before starting so the test starts from "JWT
	// exists, profile does not."
	_ = ensureAuthUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	_ = ensureAuthUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	_ = ensureAuthUser(t, supabaseURL, serviceKey, testEmailC, testPasswordC)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	tokenC := signInWithPassword(t, supabaseURL, anonKey, testEmailC, testPasswordC)
	userA := userIDFromEmail(t, supabaseURL, serviceKey, testEmailA)
	userB := userIDFromEmail(t, supabaseURL, serviceKey, testEmailB)
	userC := userIDFromEmail(t, supabaseURL, serviceKey, testEmailC)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := users.New(pool, tosCurrentVersion)

	// Build a router that mirrors the real wiring: profile/handle/avatar
	// endpoints behind auth only; /me behind both gates.
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/users/me/profile", h.PostProfile)
		r.Head("/users/handle/{handle}", h.HeadHandle)

		r.Group(func(r chi.Router) {
			r.Use(h.ProfileRequired)
			r.Post("/users/me/avatar", h.PostAvatar)

			r.Group(func(r chi.Router) {
				r.Use(h.AvatarRequired)
				r.Get("/me", auth.Me)
			})
		})
	})

	clearProfile := func(t *testing.T, userID string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `DELETE FROM public.users WHERE id = $1`, userID); err != nil {
			t.Fatalf("clear profile: %v", err)
		}
	}

	do := func(t *testing.T, method, path, token string, body any) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		var buf io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			buf = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, buf)
		if buf != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	validProfileBody := func(handle string) map[string]string {
		return map[string]string{
			"handle":         handle,
			"handle_display": handle,
			"display_name":   "User " + handle,
			"dob":            "1990-01-01",
			"tos_version":    tosCurrentVersion,
		}
	}

	t.Run("post profile: happy path returns 201, /me works after avatar", func(t *testing.T) {
		clearProfile(t, userA)

		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("usertesta"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, body)
		}

		// /me requires both gates → 409 avatar_required since no avatar.
		rec, body = do(t, http.MethodGet, "/me", tokenA, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409 before avatar, got %d body=%s", rec.Code, body)
		}
		if got := errCode(body); got != "avatar_required" {
			t.Fatalf("expected error=avatar_required, got %q", got)
		}

		// Upload avatar at the per-user path; gate clears.
		avatarBody := map[string]string{"avatar_path": userA + "/avatar.jpg"}
		rec, body = do(t, http.MethodPost, "/users/me/avatar", tokenA, avatarBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("avatar: expected 200, got %d body=%s", rec.Code, body)
		}

		rec, body = do(t, http.MethodGet, "/me", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("/me after avatar: expected 200, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("post profile: re-POST with row present returns 409 profile_complete", func(t *testing.T) {
		clearProfile(t, userA)
		rec, _ := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("usertesta"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("setup: expected 201, got %d", rec.Code)
		}

		// Same user POSTs again — even with a different handle, must NOT mutate.
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("changedhandle"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if got := errCode(body); got != "profile_complete" {
			t.Errorf("expected error=profile_complete, got %q", got)
		}

		// Confirm handle did not change.
		var handle string
		if err := pool.QueryRow(ctx, `SELECT handle FROM public.users WHERE id = $1`, userA).Scan(&handle); err != nil {
			t.Fatalf("query handle: %v", err)
		}
		if handle != "usertesta" {
			t.Errorf("re-POST mutated handle: got %q, want usertesta", handle)
		}
	})

	t.Run("post profile: handle taken by another user → 409 handle_taken", func(t *testing.T) {
		clearProfile(t, userA)
		clearProfile(t, userB)

		rec, _ := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("collide"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("setup A: expected 201, got %d", rec.Code)
		}
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, validProfileBody("collide"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if got := errCode(body); got != "handle_taken" {
			t.Errorf("expected error=handle_taken, got %q", got)
		}
	})

	t.Run("post profile: validation failures return 422", func(t *testing.T) {
		clearProfile(t, userC)
		cases := []struct {
			name string
			body map[string]string
		}{
			{"bad handle format", merge(validProfileBody("usertestc"), "handle", "Bad Handle!")},
			{"handle_display lowercase mismatch", merge(validProfileBody("usertestc"), "handle_display", "DifferentName")},
			{"empty display_name", merge(validProfileBody("usertestc"), "display_name", "")},
			{"DOB under 18", merge(validProfileBody("usertestc"), "dob", "2020-01-01")},
			{"DOB malformed", merge(validProfileBody("usertestc"), "dob", "not-a-date")},
			{"stale ToS version", merge(validProfileBody("usertestc"), "tos_version", "v0")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec, body := do(t, http.MethodPost, "/users/me/profile", tokenC, tc.body)
				if rec.Code != http.StatusUnprocessableEntity {
					t.Errorf("expected 422, got %d body=%s", rec.Code, body)
				}
			})
		}
	})

	t.Run("head handle: 200 if available, 409 if taken", func(t *testing.T) {
		clearProfile(t, userA)
		rec, _ := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("availchk"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("setup: expected 201, got %d", rec.Code)
		}

		rec, _ = do(t, http.MethodHead, "/users/handle/availchk", tokenA, nil)
		if rec.Code != http.StatusConflict {
			t.Errorf("taken handle: expected 409, got %d", rec.Code)
		}

		// Mixed-case probe still resolves (server lowercases).
		rec, _ = do(t, http.MethodHead, "/users/handle/AvailChk", tokenA, nil)
		if rec.Code != http.StatusConflict {
			t.Errorf("mixed-case probe: expected 409, got %d", rec.Code)
		}

		rec, _ = do(t, http.MethodHead, "/users/handle/freeone", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("free handle: expected 200, got %d", rec.Code)
		}

		rec, _ = do(t, http.MethodHead, "/users/handle/x!", tokenA, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("bad-format probe: expected 400, got %d", rec.Code)
		}
	})

	t.Run("post avatar: rejects path outside user prefix", func(t *testing.T) {
		clearProfile(t, userA)
		rec, _ := do(t, http.MethodPost, "/users/me/profile", tokenA, validProfileBody("usertesta"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("setup: expected 201, got %d", rec.Code)
		}

		rec, body := do(t, http.MethodPost, "/users/me/avatar", tokenA,
			map[string]string{"avatar_path": userB + "/avatar.jpg"},
		)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("foreign prefix: expected 400, got %d body=%s", rec.Code, body)
		}
	})

	t.Run("post avatar: 409 profile_required if no profile yet", func(t *testing.T) {
		clearProfile(t, userA)
		rec, body := do(t, http.MethodPost, "/users/me/avatar", tokenA,
			map[string]string{"avatar_path": userA + "/avatar.jpg"},
		)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if got := errCode(body); got != "profile_required" {
			t.Errorf("expected error=profile_required, got %q", got)
		}
	})

	t.Run("middleware: /me with no profile returns 409 profile_required", func(t *testing.T) {
		clearProfile(t, userA)
		rec, body := do(t, http.MethodGet, "/me", tokenA, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if got := errCode(body); got != "profile_required" {
			t.Errorf("expected error=profile_required, got %q", got)
		}
	})

}

func errCode(body []byte) string {
	var got struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &got)
	return got.Error
}

func merge(base map[string]string, key, val string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	out[key] = val
	return out
}

// ----- Supabase auth helpers (copy of the same helpers in
// commits_test.go / friends_test.go / etc. — extract once enough call sites
// are touched in one PR). -----

func ensureAuthUser(t *testing.T, supabaseURL, serviceKey, email, password string) string {
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
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("admin create user %s: %d %s", email, resp.StatusCode, out)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode admin create: %v", err)
	}
	return got.ID
}

func adminFindUserByEmail(t *testing.T, supabaseURL, serviceKey, email string) (string, bool) {
	t.Helper()
	url := fmt.Sprintf("%s/auth/v1/admin/users?email=%s", supabaseURL, email)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin find: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("admin find %s: %d %s", email, resp.StatusCode, out)
	}
	var got struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode admin find: %v", err)
	}
	for _, u := range got.Users {
		if u.Email == email {
			return u.ID, true
		}
	}
	return "", false
}

func userIDFromEmail(t *testing.T, supabaseURL, serviceKey, email string) string {
	t.Helper()
	id, found := adminFindUserByEmail(t, supabaseURL, serviceKey, email)
	if !found {
		t.Fatalf("user %s not found", email)
	}
	return id
}

func signInWithPassword(t *testing.T, supabaseURL, anonKey, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost,
		supabaseURL+"/auth/v1/token?grant_type=password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("password sign-in: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("password sign-in %s: %d %s", email, resp.StatusCode, out)
	}
	var got struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if got.AccessToken == "" {
		t.Fatalf("empty access token in response: %s", out)
	}
	return got.AccessToken
}
