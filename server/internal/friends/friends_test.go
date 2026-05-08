package friends_test

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
	"github.com/sasilver75/events/server/internal/friends"
)

const (
	testEmailA    = "friends-test-a@spur.local"
	testPasswordA = "friends-test-a-not-secret"
	testEmailB    = "friends-test-b@spur.local"
	testPasswordB = "friends-test-b-not-secret"
	testEmailC    = "friends-test-c@spur.local"
	testPasswordC = "friends-test-c-not-secret"

	displayNameA = "FriendTestAlice"
	displayNameB = "FriendTestBob"
	displayNameC = "FriendTestCarol"
)

type sendBody struct {
	RecipientID string `json:"recipient_id"`
}

type requestRow struct {
	Requester   string    `json:"requester"`
	Recipient   string    `json:"recipient"`
	CreatedAt   time.Time `json:"created_at"`
	DisplayName string    `json:"display_name,omitempty"`
}

type friendshipRow struct {
	UserID      string    `json:"user_id"`
	FriendID    string    `json:"friend_id"`
	CreatedAt   time.Time `json:"created_at"`
	DisplayName string    `json:"display_name,omitempty"`
}

type requestsListResponse struct {
	Incoming []requestRow `json:"incoming"`
	Outgoing []requestRow `json:"outgoing"`
}

type candidateRow struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

func TestFriendsEndpoints(t *testing.T) {
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
	tokenC := signInWithPassword(t, supabaseURL, anonKey, testEmailC, testPasswordC)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)
	userB := userIDFromToken(t, supabaseURL, serviceKey, testEmailB)
	userC := userIDFromToken(t, supabaseURL, serviceKey, testEmailC)

	setDisplayName(ctx, t, pool, userA, displayNameA)
	setDisplayName(ctx, t, pool, userB, displayNameB)
	setDisplayName(ctx, t, pool, userC, displayNameC)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := friends.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/friends", h.ListFriends)
		r.Delete("/friends/{friend_id}", h.Unfriend)
		r.Get("/friends/requests", h.ListRequests)
		r.Post("/friends/requests", h.SendRequest)
		r.Post("/friends/requests/{requester_id}/accept", h.AcceptRequest)
		r.Delete("/friends/requests/{requester_id}", h.RejectRequest)
		r.Delete("/friends/requests/sent/{recipient_id}", h.WithdrawRequest)
		r.Get("/friends/candidates", h.SearchCandidates)
	})

	resetGraph := func() {
		t.Helper()
		ids := []string{userA, userB, userC}
		if _, err := pool.Exec(ctx, `
			DELETE FROM public.friendships
			WHERE user_id = ANY($1) OR friend_id = ANY($1)
		`, ids); err != nil {
			t.Fatalf("reset friendships: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			DELETE FROM public.friendship_requests
			WHERE requester = ANY($1) OR recipient = ANY($1)
		`, ids); err != nil {
			t.Fatalf("reset requests: %v", err)
		}
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

	t.Run("missing token returns 401 across surface", func(t *testing.T) {
		resetGraph()
		paths := []struct {
			method, path string
		}{
			{http.MethodGet, "/friends"},
			{http.MethodGet, "/friends/requests"},
			{http.MethodPost, "/friends/requests"},
			{http.MethodGet, "/friends/candidates?q=" + displayNameB},
			{http.MethodPost, "/friends/requests/" + userA + "/accept"},
			{http.MethodDelete, "/friends/requests/" + userA},
			{http.MethodDelete, "/friends/requests/sent/" + userB},
			{http.MethodDelete, "/friends/" + userB},
		}
		for _, p := range paths {
			rec, _ := do(t, p.method, p.path, "", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s: expected 401, got %d", p.method, p.path, rec.Code)
			}
		}
	})

	t.Run("send: happy path", func(t *testing.T) {
		resetGraph()
		rec, body := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: userB})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, body)
		}
		var rr requestRow
		if err := json.Unmarshal(body, &rr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if rr.Requester != userA || rr.Recipient != userB {
			t.Errorf("unexpected row: %+v", rr)
		}
		if rr.CreatedAt.IsZero() {
			t.Errorf("created_at zero")
		}
	})

	t.Run("send: cannot friend yourself", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: userA})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("send: 409 if already friends", func(t *testing.T) {
		resetGraph()
		insertFriendshipPair(ctx, t, pool, userA, userB)
		rec, body := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: userB})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("already_friends")) {
			t.Errorf("expected already_friends, got %s", body)
		}
	})

	t.Run("send: 409 if same-direction request already sent", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)
		rec, body := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: userB})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("request_already_sent")) {
			t.Errorf("expected request_already_sent, got %s", body)
		}
	})

	t.Run("send: 409 if reverse-direction request pending", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userB, userA)
		rec, body := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: userB})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rec.Code, body)
		}
		if !bytes.Contains(body, []byte("request_pending_from_them")) {
			t.Errorf("expected request_pending_from_them, got %s", body)
		}
	})

	t.Run("send: 404 if recipient does not exist", func(t *testing.T) {
		resetGraph()
		ghost := "11111111-1111-1111-1111-111111111111"
		rec, _ := do(t, http.MethodPost, "/friends/requests", tokenA, sendBody{RecipientID: ghost})
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("accept: writes both mirror rows in one tx and deletes the request", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)

		rec, body := do(t, http.MethodPost, "/friends/requests/"+userA+"/accept", tokenB, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var fr friendshipRow
		if err := json.Unmarshal(body, &fr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if fr.UserID != userB || fr.FriendID != userA {
			t.Errorf("unexpected caller half: %+v", fr)
		}
		assertFriendshipExists(ctx, t, pool, userA, userB)
		assertFriendshipExists(ctx, t, pool, userB, userA)
		assertRequestAbsent(ctx, t, pool, userA, userB)
	})

	t.Run("accept: 404 if no matching request", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodPost, "/friends/requests/"+userA+"/accept", tokenB, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("accept: mid-tx-failure rolls back fully — request preserved, no partial mirror", func(t *testing.T) {
		// Provoke a PK violation on the inner INSERT by pre-inserting the
		// caller's mirror half. The accept handler will: delete request →
		// insert (caller, requester) which collides → rollback.
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)
		insertOneSidedFriendship(ctx, t, pool, userB, userA) // caller=B's half pre-exists

		rec, _ := do(t, http.MethodPost, "/friends/requests/"+userA+"/accept", tokenB, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 on mid-tx PK violation, got %d", rec.Code)
		}
		// Request must still exist (delete rolled back).
		assertRequestExists(ctx, t, pool, userA, userB)
		// The pre-inserted half stays; the other half was never written.
		assertFriendshipExists(ctx, t, pool, userB, userA)
		assertFriendshipAbsent(ctx, t, pool, userA, userB)
	})

	t.Run("reject: deletes the incoming request", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)

		rec, _ := do(t, http.MethodDelete, "/friends/requests/"+userA, tokenB, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		assertRequestAbsent(ctx, t, pool, userA, userB)
	})

	t.Run("reject: 404 if no matching incoming request", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodDelete, "/friends/requests/"+userA, tokenB, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("reject: requester cannot reject their own outgoing request via the recipient route", func(t *testing.T) {
		// The reject path filters by recipient = caller; caller=A querying
		// "DELETE /friends/requests/{requester=A}" looks for a row where
		// requester=A AND recipient=A, which is impossible.
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)
		rec, _ := do(t, http.MethodDelete, "/friends/requests/"+userA, tokenA, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
		assertRequestExists(ctx, t, pool, userA, userB)
	})

	t.Run("withdraw: deletes the outgoing request", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)

		rec, _ := do(t, http.MethodDelete, "/friends/requests/sent/"+userB, tokenA, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		assertRequestAbsent(ctx, t, pool, userA, userB)
	})

	t.Run("withdraw: 404 if no matching outgoing request", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodDelete, "/friends/requests/sent/"+userB, tokenA, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("withdraw: recipient cannot withdraw via the requester route", func(t *testing.T) {
		// Symmetric to the reject test: this path filters by requester=caller.
		resetGraph()
		insertRequest(ctx, t, pool, userA, userB)
		rec, _ := do(t, http.MethodDelete, "/friends/requests/sent/"+userA, tokenB, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
		assertRequestExists(ctx, t, pool, userA, userB)
	})

	t.Run("unfriend: deletes both mirror rows in one tx", func(t *testing.T) {
		resetGraph()
		insertFriendshipPair(ctx, t, pool, userA, userB)

		rec, _ := do(t, http.MethodDelete, "/friends/"+userB, tokenA, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		assertFriendshipAbsent(ctx, t, pool, userA, userB)
		assertFriendshipAbsent(ctx, t, pool, userB, userA)
	})

	t.Run("unfriend: 404 if not friends", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodDelete, "/friends/"+userB, tokenA, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("list friends: returns rows with display names", func(t *testing.T) {
		resetGraph()
		insertFriendshipPair(ctx, t, pool, userA, userB)
		insertFriendshipPair(ctx, t, pool, userA, userC)

		rec, body := do(t, http.MethodGet, "/friends", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var got []friendshipRow
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 friends, got %d: %+v", len(got), got)
		}
		seen := map[string]string{}
		for _, f := range got {
			if f.UserID != userA {
				t.Errorf("unexpected user_id: %s", f.UserID)
			}
			seen[f.FriendID] = f.DisplayName
		}
		if seen[userB] != displayNameB {
			t.Errorf("expected display_name=%s for userB, got %q", displayNameB, seen[userB])
		}
		if seen[userC] != displayNameC {
			t.Errorf("expected display_name=%s for userC, got %q", displayNameC, seen[userC])
		}
	})

	t.Run("list requests: returns incoming and outgoing", func(t *testing.T) {
		resetGraph()
		insertRequest(ctx, t, pool, userB, userA) // incoming for A
		insertRequest(ctx, t, pool, userA, userC) // outgoing from A

		rec, body := do(t, http.MethodGet, "/friends/requests", tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var got requestsListResponse
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Incoming) != 1 || got.Incoming[0].Requester != userB || got.Incoming[0].DisplayName != displayNameB {
			t.Errorf("incoming wrong: %+v", got.Incoming)
		}
		if len(got.Outgoing) != 1 || got.Outgoing[0].Recipient != userC || got.Outgoing[0].DisplayName != displayNameC {
			t.Errorf("outgoing wrong: %+v", got.Outgoing)
		}
	})

	t.Run("candidates: exact match returns user; excludes caller", func(t *testing.T) {
		resetGraph()
		rec, body := do(t, http.MethodGet, "/friends/candidates?q="+displayNameB, tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var got []candidateRow
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].UserID != userB || got[0].DisplayName != displayNameB {
			t.Errorf("expected one B candidate, got %+v", got)
		}

		// Caller searching for their own display name → empty.
		rec, body = do(t, http.MethodGet, "/friends/candidates?q="+displayNameA, tokenA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, body)
		}
		var self []candidateRow
		_ = json.Unmarshal(body, &self)
		if len(self) != 0 {
			t.Errorf("self should not appear in own candidates: %+v", self)
		}
	})

	t.Run("candidates: q required", func(t *testing.T) {
		resetGraph()
		rec, _ := do(t, http.MethodGet, "/friends/candidates", tokenA, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	// Silence unused-warning for tokenC in the rare branches that don't use it.
	_ = tokenC
}

// --- DB helpers ---

func setDisplayName(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID, name string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE public.users SET display_name = $1 WHERE id = $2`, name, userID); err != nil {
		t.Fatalf("set display_name: %v", err)
	}
}

func insertRequest(ctx context.Context, t *testing.T, pool *pgxpool.Pool, requester, recipient string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.friendship_requests (requester, recipient) VALUES ($1, $2)
	`, requester, recipient); err != nil {
		t.Fatalf("insert request: %v", err)
	}
}

func insertFriendshipPair(ctx context.Context, t *testing.T, pool *pgxpool.Pool, a, b string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.friendships (user_id, friend_id) VALUES ($1, $2), ($2, $1)
	`, a, b); err != nil {
		t.Fatalf("insert friendship pair: %v", err)
	}
}

func insertOneSidedFriendship(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID, friendID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.friendships (user_id, friend_id) VALUES ($1, $2)
	`, userID, friendID); err != nil {
		t.Fatalf("insert one-sided friendship: %v", err)
	}
}

func assertFriendshipExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID, friendID string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.friendships WHERE user_id=$1 AND friend_id=$2)
	`, userID, friendID).Scan(&exists); err != nil {
		t.Fatalf("check friendship: %v", err)
	}
	if !exists {
		t.Errorf("expected friendship (%s, %s) to exist", userID, friendID)
	}
}

func assertFriendshipAbsent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID, friendID string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.friendships WHERE user_id=$1 AND friend_id=$2)
	`, userID, friendID).Scan(&exists); err != nil {
		t.Fatalf("check friendship: %v", err)
	}
	if exists {
		t.Errorf("expected friendship (%s, %s) to be absent", userID, friendID)
	}
}

func assertRequestExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, requester, recipient string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.friendship_requests WHERE requester=$1 AND recipient=$2)
	`, requester, recipient).Scan(&exists); err != nil {
		t.Fatalf("check request: %v", err)
	}
	if !exists {
		t.Errorf("expected request (%s → %s) to exist", requester, recipient)
	}
}

func assertRequestAbsent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, requester, recipient string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.friendship_requests WHERE requester=$1 AND recipient=$2)
	`, requester, recipient).Scan(&exists); err != nil {
		t.Fatalf("check request: %v", err)
	}
	if exists {
		t.Errorf("expected request (%s → %s) to be absent", requester, recipient)
	}
}

// --- Supabase auth helpers (copy of the same helpers in checkins_test.go and
// commits_test.go; per the comment over there, extract once a fourth call
// site appears). ---

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
