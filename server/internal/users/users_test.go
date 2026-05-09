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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/legal"
	"github.com/sasilver75/events/server/internal/users"
)

// Three test users so handle-collision and middleware-gating subtests
// don't compete for the same email. Emails differ from friends-test-*
// and commits-test-* / checkins-test-* so concurrent test runs don't
// collide on Supabase Admin API state.
const (
	testEmailA    = "users-test-a@spur.local"
	testPasswordA = "users-test-a-not-secret"
	testEmailB    = "users-test-b@spur.local"
	testPasswordB = "users-test-b-not-secret"
	testEmailC    = "users-test-c@spur.local"
	testPasswordC = "users-test-c-not-secret"
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

	userA := ensureTestUser(t, supabaseURL, serviceKey, testEmailA, testPasswordA)
	userB := ensureTestUser(t, supabaseURL, serviceKey, testEmailB, testPasswordB)
	userC := ensureTestUser(t, supabaseURL, serviceKey, testEmailC, testPasswordC)
	tokenA := signInWithPassword(t, supabaseURL, anonKey, testEmailA, testPasswordA)
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	tokenC := signInWithPassword(t, supabaseURL, anonKey, testEmailC, testPasswordC)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := users.New(pool)

	// Mount the same route shape main.go uses: public surface, then
	// JWT-only group with the profile upsert, then JWT + RequireProfile.
	r := chi.NewRouter()
	r.Get("/legal/tos", legal.Get)
	r.Head("/users/handle/{handle}", h.HandleProbe)
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/users/me/profile", h.UpsertProfile)
		r.Group(func(r chi.Router) {
			r.Use(users.RequireProfile(pool))
			r.Get("/me", auth.Me)
			r.Post("/users/me/avatar", h.SetAvatar)
		})
	})

	// Wipe profile rows for the three test users at the start so each run
	// starts clean. CASCADE drops dependent rows in events/commits/etc.,
	// but these test users don't host any seed events.
	if _, err := pool.Exec(ctx, `
		DELETE FROM public.users WHERE id = ANY($1)
	`, []string{userA, userB, userC}); err != nil {
		t.Fatalf("clear profiles: %v", err)
	}

	do := func(t *testing.T, method, path, token string, body any) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, reader)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	dobOK := "1990-01-01"

	t.Run("GET /legal/tos returns version + content (public, no JWT)", func(t *testing.T) {
		rec, body := do(t, http.MethodGet, "/legal/tos", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var got struct {
			Version string `json:"version"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Version != legal.Version {
			t.Errorf("version mismatch: want %q got %q", legal.Version, got.Version)
		}
		if len(got.Content) < 100 {
			t.Errorf("content suspiciously short: %d bytes", len(got.Content))
		}
	})

	t.Run("RequireProfile gates GET /me before profile exists (409 profile_required)", func(t *testing.T) {
		rec, body := do(t, http.MethodGet, "/me", tokenA, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409 before profile, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("profile_required")) {
			t.Errorf("expected profile_required, got %s", body)
		}
	})

	t.Run("HEAD /users/handle/{handle} returns 200 when available", func(t *testing.T) {
		rec, _ := do(t, http.MethodHead, "/users/handle/userstesta", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 (available) before any profile, got %d", rec.Code)
		}
	})

	t.Run("HEAD /users/handle/{bad} returns 422 for malformed input", func(t *testing.T) {
		rec, _ := do(t, http.MethodHead, "/users/handle/x", "", nil) // too short
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for short handle, got %d", rec.Code)
		}
		rec, _ = do(t, http.MethodHead, "/users/handle/has-dash", "", nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for dash, got %d", rec.Code)
		}
	})

	t.Run("POST /users/me/profile creates the row (happy path)", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenA, profileBody{
			Handle:        "userstesta",
			HandleDisplay: "UsersTestA",
			DisplayName:   "Users Test A",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var got profileResp
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.UserID != userA || got.Handle != "userstesta" || got.HandleDisplay != "UsersTestA" {
			t.Errorf("unexpected response: %+v", got)
		}

		// And now public.users has the row with all required fields.
		var dbRow struct {
			Handle, HandleDisplay, DisplayName, TosVersion string
		}
		err := pool.QueryRow(ctx, `
			SELECT handle, handle_display, display_name, tos_version
			FROM public.users WHERE id = $1
		`, userA).Scan(&dbRow.Handle, &dbRow.HandleDisplay, &dbRow.DisplayName, &dbRow.TosVersion)
		if err != nil {
			t.Fatalf("verify row: %v", err)
		}
		if dbRow.Handle != "userstesta" || dbRow.TosVersion != legal.Version {
			t.Errorf("DB row mismatch: %+v", dbRow)
		}
	})

	t.Run("RequireProfile lets GET /me through after profile is created", func(t *testing.T) {
		rec, body := do(t, http.MethodGet, "/me", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 after profile, got %d: %s", rec.Code, body)
		}
		var got map[string]string
		_ = json.Unmarshal(body, &got)
		if got["user_id"] != userA {
			t.Errorf("user_id mismatch: %s", got["user_id"])
		}
	})

	t.Run("HEAD /users/handle/{taken} returns 409", func(t *testing.T) {
		// The previous subtest claimed `userstesta` for userA.
		rec, _ := do(t, http.MethodHead, "/users/handle/userstesta", "", nil)
		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 (taken), got %d", rec.Code)
		}
	})

	t.Run("HEAD /users/handle case-insensitive match", func(t *testing.T) {
		// Server normalizes incoming handle to lowercase before the lookup.
		rec, _ := do(t, http.MethodHead, "/users/handle/UsersTestA", "", nil)
		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 for mixed-case match on existing handle, got %d", rec.Code)
		}
	})

	t.Run("POST /users/me/profile second user attempting userA's handle → 409 handle_taken", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "userstesta",
			HandleDisplay: "UsersTestA",
			DisplayName:   "Different person",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("handle_taken")) {
			t.Errorf("expected handle_taken, got %s", body)
		}
	})

	t.Run("POST /users/me/profile rejects bad handle format (422 handle_format)", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "bad-handle!", // dash + bang outside [a-z0-9_]
			HandleDisplay: "bad-handle!",
			DisplayName:   "B",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("handle_format")) {
			t.Errorf("expected handle_format, got %s", body)
		}
	})

	t.Run("POST /users/me/profile rejects handle_display whose lower != handle (422)", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "userstestb",
			HandleDisplay: "DifferentText", // lower("DifferentText") != "userstestb"
			DisplayName:   "B",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("handle_display_mismatch")) {
			t.Errorf("expected handle_display_mismatch, got %s", body)
		}
	})

	t.Run("POST /users/me/profile rejects empty display_name (422)", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "userstestb",
			HandleDisplay: "userstestb",
			DisplayName:   "   ",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("display_name_empty")) {
			t.Errorf("expected display_name_empty, got %s", body)
		}
	})

	t.Run("POST /users/me/profile rejects DOB < 18 years ago (422)", func(t *testing.T) {
		recent := time.Now().AddDate(-17, 0, 0).Format("2006-01-02")
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "userstestb",
			HandleDisplay: "userstestb",
			DisplayName:   "B",
			DOB:           recent,
			TosVersion:    legal.Version,
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("dob_too_recent")) {
			t.Errorf("expected dob_too_recent, got %s", body)
		}
	})

	t.Run("POST /users/me/profile rejects unknown tos_version (422)", func(t *testing.T) {
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenB, profileBody{
			Handle:        "userstestb",
			HandleDisplay: "userstestb",
			DisplayName:   "B",
			DOB:           dobOK,
			TosVersion:    "v999",
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("tos_version_mismatch")) {
			t.Errorf("expected tos_version_mismatch, got %s", body)
		}
	})

	t.Run("POST /users/me/profile is idempotent at the JWT subject (retry → 200)", func(t *testing.T) {
		// userA already has a profile from earlier. Submitting the same
		// payload again should succeed (signup-flow retry safety).
		input := profileBody{
			Handle:        "userstesta",
			HandleDisplay: "UsersTestA",
			DisplayName:   "Users Test A",
			DOB:           dobOK,
			TosVersion:    legal.Version,
		}
		rec, body := do(t, http.MethodPost, "/users/me/profile", tokenA, input)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on retry, got %d: %s", rec.Code, body)
		}
		// And again — still 200.
		rec, body = do(t, http.MethodPost, "/users/me/profile", tokenA, input)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on second retry, got %d: %s", rec.Code, body)
		}
	})

	t.Run("POST /users/me/avatar requires profile (gated by middleware)", func(t *testing.T) {
		// userC has no profile yet — the middleware should reject before
		// the handler runs.
		rec, body := do(t, http.MethodPost, "/users/me/avatar", tokenC, map[string]string{
			"path": userC + "/whatever.jpg",
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409 profile_required, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("profile_required")) {
			t.Errorf("expected profile_required, got %s", body)
		}
	})

	t.Run("POST /users/me/avatar happy path writes avatar_path", func(t *testing.T) {
		path := userA + "/avatar-test.jpg"
		rec, body := do(t, http.MethodPost, "/users/me/avatar", tokenA, map[string]string{
			"path": path,
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d: %s", rec.Code, body)
		}
		var stored string
		err := pool.QueryRow(ctx, `SELECT avatar_path FROM public.users WHERE id = $1`, userA).Scan(&stored)
		if err != nil {
			t.Fatalf("read avatar_path: %v", err)
		}
		if stored != path {
			t.Errorf("avatar_path mismatch: want %q got %q", path, stored)
		}
	})

	t.Run("POST /users/me/avatar rejects path not under caller's prefix (403)", func(t *testing.T) {
		// userA tries to claim an avatar under userB's prefix.
		rec, body := do(t, http.MethodPost, "/users/me/avatar", tokenA, map[string]string{
			"path": userB + "/sneaky.jpg",
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("avatar_path_not_owned")) {
			t.Errorf("expected avatar_path_not_owned, got %s", body)
		}
	})

	// Silence unused-warning for tokenC outside its specific subtest.
	_ = tokenC
}

// --- request/response shapes (kept local — wire mirror of users.go) ---

type profileBody struct {
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
	DOB           string `json:"dob"`
	TosVersion    string `json:"tos_version"`
}

type profileResp struct {
	UserID        string `json:"user_id"`
	Handle        string `json:"handle"`
	HandleDisplay string `json:"handle_display"`
	DisplayName   string `json:"display_name"`
}

// --- Supabase auth helpers (copy of the same helpers in checkins/, commits/,
// and friends/ tests; the comment over there said "extract once a fourth
// call site appears" — this is the fourth, but extracting cuts across four
// packages and the diff cost outweighs the dedupe at this scope. Track the
// extraction as a follow-up cleanup, not load-bearing for #88). ---

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
