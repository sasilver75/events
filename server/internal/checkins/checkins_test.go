package checkins_test

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
	"github.com/sasilver75/events/server/internal/checkins"
)

const (
	testEmailA    = "checkins-test-a@spur.local"
	testPasswordA = "checkins-test-a-not-secret"
	testEmailB    = "checkins-test-b@spur.local"
	testPasswordB = "checkins-test-b-not-secret"
)

// Pin coordinate used by all tests. Same downtown LA point used by
// commits_test.go so reading both side-by-side is straightforward.
const (
	pinLat = 34.0522
	pinLon = -118.2437
)

type checkinResponse struct {
	RecordedAt time.Time `json:"recorded_at"`
}

type errorBody struct {
	Error     string  `json:"error"`
	DistanceM float64 `json:"distance_m"`
	AccuracyM float64 `json:"accuracy_m"`
}

type checkinRequest struct {
	Lat                 float64 `json:"lat"`
	Lon                 float64 `json:"lon"`
	HorizontalAccuracyM float64 `json:"horizontal_accuracy_m"`
}

func TestCheckInEndpoint(t *testing.T) {
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
	tokenB := signInWithPassword(t, supabaseURL, anonKey, testEmailB, testPasswordB)
	userA := userIDFromToken(t, supabaseURL, serviceKey, testEmailA)
	userB := userIDFromToken(t, supabaseURL, serviceKey, testEmailB)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := checkins.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/events/{id}/checkin", h.CheckIn)
	})

	hostID := seedHostID(ctx, t, pool)

	doRequest := func(t *testing.T, eventID, token string, body checkinRequest) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/checkin", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out, _ := io.ReadAll(rec.Body)
		return rec, out
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })

		raw, _ := json.Marshal(checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		req := httptest.NewRequest(http.MethodPost, "/events/"+eventID+"/checkin", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid event id returns 400", func(t *testing.T) {
		rec, _ := doRequest(t, "not-a-uuid", tokenA, checkinRequest{Lat: pinLat, Lon: pinLon})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown event returns 404", func(t *testing.T) {
		const bogus = "00000000-0000-0000-0000-000000000000"
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, _ := doRequest(t, bogus, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon})
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("non-attendee returns 403 not_attendee", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })

		rec, body := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
		if e := decodeError(t, body); e.Error != "not_attendee" {
			t.Errorf("error: got %q, want %q", e.Error, "not_attendee")
		}
	})

	t.Run("event not Live returns 409 not_live", func(t *testing.T) {
		// Future-only event → state='Open' (no commits yet) or 'Filling' once
		// userA commits. Either way, not 'Live'.
		eventID := insertFutureEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, body := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		if e := decodeError(t, body); e.Error != "not_live" {
			t.Errorf("error: got %q, want %q", e.Error, "not_live")
		}
	})

	t.Run("happy path: at pin with confident fix returns 200", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, body := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		var got checkinResponse
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		if got.RecordedAt.IsZero() {
			t.Errorf("recorded_at is zero")
		}

		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.checkins WHERE event_id = $1 AND user_id = $2`,
			eventID, userA,
		).Scan(&n); err != nil {
			t.Fatalf("count checkins: %v", err)
		}
		if n != 1 {
			t.Errorf("DB row count: got %d, want 1", n)
		}
	})

	t.Run("idempotent: repeat tap returns 200 and preserves recorded_at", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		_, first := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		var firstRow checkinResponse
		_ = json.Unmarshal(first, &firstRow)

		// Sleep so any "now()" recompute would observably differ from the
		// first row's recorded_at — the assertion is that the original
		// timestamp survives, not just that it falls in some window.
		time.Sleep(20 * time.Millisecond)

		rec, second := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, second)
		}
		var secondRow checkinResponse
		_ = json.Unmarshal(second, &secondRow)
		if !firstRow.RecordedAt.Equal(secondRow.RecordedAt) {
			t.Errorf("recorded_at changed on repeat tap: %v → %v",
				firstRow.RecordedAt, secondRow.RecordedAt)
		}

		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.checkins WHERE event_id = $1 AND user_id = $2`,
			eventID, userA,
		).Scan(&n)
		if n != 1 {
			t.Errorf("DB row count: got %d, want 1", n)
		}
	})

	// ADR 0011: the spoof-zone case. A confident fix (5m) at 60m from the
	// pin must be rejected — accuracy-aware floor is 50m, and 60−5 > 50.
	t.Run("confident fix at 60m from pin rejects with 409 not_at_event", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		far := offsetMetersNorth(pinLat, pinLon, 60)
		rec, body := doRequest(t, eventID, tokenA, checkinRequest{
			Lat: far.lat, Lon: far.lon, HorizontalAccuracyM: 5,
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", rec.Code, body)
		}
		e := decodeError(t, body)
		if e.Error != "not_at_event" {
			t.Errorf("error: got %q, want %q", e.Error, "not_at_event")
		}
		if e.DistanceM < 55 || e.DistanceM > 65 {
			t.Errorf("distance_m: got %.1f, want ~60", e.DistanceM)
		}
		if e.AccuracyM != 5 {
			t.Errorf("accuracy_m: got %.1f, want 5", e.AccuracyM)
		}
	})

	// ADR 0011: the indoor-honest case. An honest ±80m fix at 30m from the
	// pin must be accepted — 30−80 ≤ 50.
	t.Run("honest indoor uncertainty at 30m with accuracy=80m accepts", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userB)

		near := offsetMetersNorth(pinLat, pinLon, 30)
		rec, body := doRequest(t, eventID, tokenB, checkinRequest{
			Lat: near.lat, Lon: near.lon, HorizontalAccuracyM: 80,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
	})

	// #65 first-presence wire: a successful check-in inserts a single
	// kind='system' chat message; a repeat tap does NOT insert another.
	t.Run("first check-in fires a system message; repeat does not", func(t *testing.T) {
		eventID := insertLiveEvent(ctx, t, pool, hostID)
		t.Cleanup(func() { deleteEvent(ctx, t, pool, eventID) })
		insertCommit(ctx, t, pool, eventID, userA)

		rec, body := doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusOK {
			t.Fatalf("first tap: expected 200, got %d body=%s", rec.Code, body)
		}
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.event_messages WHERE event_id = $1 AND kind = 'system'`,
			eventID,
		).Scan(&n); err != nil {
			t.Fatalf("count system messages: %v", err)
		}
		if n != 1 {
			t.Errorf("after first tap: got %d system messages, want 1", n)
		}

		// Repeat tap — idempotent path, should NOT fire a second message.
		rec, _ = doRequest(t, eventID, tokenA, checkinRequest{Lat: pinLat, Lon: pinLon, HorizontalAccuracyM: 5})
		if rec.Code != http.StatusOK {
			t.Fatalf("repeat tap: expected 200, got %d", rec.Code)
		}
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM public.event_messages WHERE event_id = $1 AND kind = 'system'`,
			eventID,
		).Scan(&n)
		if n != 1 {
			t.Errorf("after repeat tap: got %d system messages, want still 1", n)
		}
	})
}

type latLon struct{ lat, lon float64 }

// offsetMetersNorth shifts a lat/lon by approximately n meters due north.
// 1 degree of latitude ≈ 111,111m, which is accurate enough for the
// 30–60m geofence cases we exercise here.
func offsetMetersNorth(lat, lon float64, meters float64) latLon {
	return latLon{lat: lat + meters/111_111.0, lon: lon}
}

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
	var hostID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&hostID); err != nil {
		t.Fatalf("find spur-seed host: %v", err)
	}
	return hostID
}

// insertLiveEvent inserts an event whose [start_time, end_time) bracket
// now() so event_state(e) returns 'Live' for the duration of the test.
func insertLiveEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'checkins live', 'checkins test', 'Other',
			now() - interval '15 minutes', now() + interval '1 hour', 4,
			ST_SetSRID(ST_MakePoint($2, $3), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID, pinLon, pinLat).Scan(&id)
	if err != nil {
		t.Fatalf("insert live event: %v", err)
	}
	return id
}

func insertFutureEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'checkins future', 'checkins test', 'Other',
			now() + interval '1 hour', now() + interval '2 hours', 4,
			ST_SetSRID(ST_MakePoint($2, $3), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID, pinLon, pinLat).Scan(&id)
	if err != nil {
		t.Fatalf("insert future event: %v", err)
	}
	return id
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

// ensureTestUser idempotently creates a Supabase user via Admin API.
// Mirror of the helper in commits_test.go; once a fourth call site
// appears, extract.
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
