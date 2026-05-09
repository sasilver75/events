package events_test

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
	"github.com/sasilver75/events/server/internal/events"
)

const (
	testEmail    = "events-test@spur.local"
	testPassword = "events-test-not-secret"
)

type nearbyEvent struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Category      string    `json:"category"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Lat           float64   `json:"lat"`
	Lon           float64   `json:"lon"`
	Cap           *int      `json:"cap"`
	CommitCount   int       `json:"commit_count"`
	CommittedByMe bool      `json:"committed_by_me"`
	State         string    `json:"state"`
}

func TestNearbyEventsEndpoint(t *testing.T) {
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

	requireSeedEvents(ctx, t, pool)

	viewerID := ensureTestUser(t, supabaseURL, serviceKey)
	// ADR 0025 dropped the auth-mirror trigger; explicitly create the
	// public.users row the FK on commits requires for this viewer.
	ensureProfile(ctx, t, pool, viewerID, "eventstestv", "EventsTestV", "EventsTestV")
	if _, err := pool.Exec(ctx, `DELETE FROM public.commits WHERE user_id = $1`, viewerID); err != nil {
		t.Fatalf("clear commits: %v", err)
	}
	token := signInWithPassword(t, supabaseURL, anonKey)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/events", events.New(pool).Near)
	})

	get := func(t *testing.T, query string) (*httptest.ResponseRecorder, []nearbyEvent) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/events?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var body []nearbyEvent
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v: %s", err, rec.Body.String())
			}
		}
		return rec, body
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/events?near=34.05,-118.24", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing near param returns 400", func(t *testing.T) {
		rec, _ := get(t, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("LA-area returns all 3 seed events", func(t *testing.T) {
		rec, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		if len(body) < 3 {
			t.Errorf("expected ≥3 events, got %d", len(body))
		}
		validStates := map[string]bool{
			"Open": true, "Filling": true, "Tipped": true,
			"Live": true, "Done": true, "Cancelled": true,
		}
		for _, e := range body {
			if !validStates[e.State] {
				t.Errorf("event %s: state %q is not one of the six valid values", e.ID, e.State)
			}
		}
	})

	t.Run("far away returns empty array, not null", func(t *testing.T) {
		rec, _ := get(t, "near=40.7128,-74.0060&radius_m=1000") // NYC
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "[]\n" {
			t.Errorf("expected empty JSON array, got %q", rec.Body.String())
		}
	})

	t.Run("bad from returns 400", func(t *testing.T) {
		rec, _ := get(t, "near=34.0522,-118.2437&from=not-a-time")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("to <= from returns 400", func(t *testing.T) {
		from := time.Now().UTC().Format(time.RFC3339)
		to := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		rec, _ := get(t, fmt.Sprintf("near=34.0522,-118.2437&from=%s&to=%s", from, to))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("event outside window is excluded", func(t *testing.T) {
		farID := insertWindowSeedEvent(ctx, t, pool, 60*time.Hour, 61*time.Hour)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, farID)
		})

		toClose := time.Now().UTC().Add(5 * time.Hour).Format(time.RFC3339)
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000&to="+toClose)
		for _, e := range body {
			if e.ID == farID {
				t.Errorf("event 60h out should be excluded by to=%s", toClose)
			}
		}
	})

	t.Run("event in progress is included when its end is after from", func(t *testing.T) {
		// start 30 min ago, end 30 min from now: a Live event.
		liveID := insertWindowSeedEvent(ctx, t, pool, -30*time.Minute, 30*time.Minute)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, liveID)
		})

		// Window starting "now": event started before from, but end > from.
		fromNow := time.Now().UTC().Format(time.RFC3339)
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000&from="+fromNow)
		var seen bool
		for _, e := range body {
			if e.ID == liveID {
				seen = true
			}
		}
		if !seen {
			t.Errorf("Live event should overlap window from=%s", fromNow)
		}
	})

	t.Run("response includes end_time", func(t *testing.T) {
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		if len(body) == 0 {
			t.Fatalf("no events returned")
		}
		if body[0].EndTime.IsZero() {
			t.Errorf("end_time missing from response")
		}
	})

	t.Run("past event is excluded", func(t *testing.T) {
		pastID := insertPastSeedEvent(ctx, t, pool)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, pastID)
		})

		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		for _, e := range body {
			if e.ID == pastID {
				t.Errorf("past event %s should be excluded", pastID)
			}
		}
	})

	publicEvent := findEventByKey(ctx, t, pool, "la-venice-beach-pickup-basketball")
	fuzzedEvent := findEventByKey(ctx, t, pool, "la-silverlake-intelligentsia-coffee")

	t.Run("public event returns exact center for non-Committed viewer", func(t *testing.T) {
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got := pickByID(body, publicEvent.id)
		if got == nil {
			t.Fatalf("public event %s missing from response", publicEvent.id)
		}
		if got.Lat != publicEvent.centerLat || got.Lon != publicEvent.centerLon {
			t.Errorf("public coords drift: got (%f, %f), want (%f, %f)",
				got.Lat, got.Lon, publicEvent.centerLat, publicEvent.centerLon)
		}
	})

	t.Run("fuzzed event returns display_geom for non-Committed viewer", func(t *testing.T) {
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got := pickByID(body, fuzzedEvent.id)
		if got == nil {
			t.Fatalf("fuzzed event %s missing from response", fuzzedEvent.id)
		}
		if got.Lat == fuzzedEvent.centerLat && got.Lon == fuzzedEvent.centerLon {
			t.Errorf("fuzzed event leaked exact center to non-Committed viewer")
		}
		if got.Lat != fuzzedEvent.displayLat || got.Lon != fuzzedEvent.displayLon {
			t.Errorf("fuzzed coords are not display_geom: got (%f, %f), want (%f, %f)",
				got.Lat, got.Lon, fuzzedEvent.displayLat, fuzzedEvent.displayLon)
		}
	})

	t.Run("fuzzed coords are stable across requests (set-once)", func(t *testing.T) {
		_, body1 := get(t, "near=34.0522,-118.2437&radius_m=30000")
		_, body2 := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got1 := pickByID(body1, fuzzedEvent.id)
		got2 := pickByID(body2, fuzzedEvent.id)
		if got1 == nil || got2 == nil {
			t.Fatalf("fuzzed event missing from one of the responses")
		}
		if got1.Lat != got2.Lat || got1.Lon != got2.Lon {
			t.Errorf("set-once violated: (%f,%f) → (%f,%f)", got1.Lat, got1.Lon, got2.Lat, got2.Lon)
		}
	})

	t.Run("committed_by_me is false when viewer has no commit row", func(t *testing.T) {
		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got := pickByID(body, publicEvent.id)
		if got == nil {
			t.Fatalf("public event %s missing from response", publicEvent.id)
		}
		if got.CommittedByMe {
			t.Errorf("committed_by_me: got true, want false (no commit row)")
		}
	})

	t.Run("committed_by_me is true after viewer Commits", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			publicEvent.id, viewerID,
		); err != nil {
			t.Fatalf("insert commit: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx,
				`DELETE FROM public.commits WHERE event_id = $1 AND user_id = $2`,
				publicEvent.id, viewerID,
			)
		})

		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got := pickByID(body, publicEvent.id)
		if got == nil {
			t.Fatalf("public event %s missing from response", publicEvent.id)
		}
		if !got.CommittedByMe {
			t.Errorf("committed_by_me: got false, want true (viewer Committed)")
		}
	})

	t.Run("fuzzed event returns exact center after viewer Commits", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			fuzzedEvent.id, viewerID,
		); err != nil {
			t.Fatalf("insert commit: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx,
				`DELETE FROM public.commits WHERE event_id = $1 AND user_id = $2`,
				fuzzedEvent.id, viewerID,
			)
		})

		_, body := get(t, "near=34.0522,-118.2437&radius_m=30000")
		got := pickByID(body, fuzzedEvent.id)
		if got == nil {
			t.Fatalf("fuzzed event missing from response")
		}
		if got.Lat != fuzzedEvent.centerLat || got.Lon != fuzzedEvent.centerLon {
			t.Errorf("Committed viewer should see exact center: got (%f, %f), want (%f, %f)",
				got.Lat, got.Lon, fuzzedEvent.centerLat, fuzzedEvent.centerLon)
		}
	})
}

type seedEventCoords struct {
	id         string
	centerLat  float64
	centerLon  float64
	displayLat float64
	displayLon float64
}

func findEventByKey(ctx context.Context, t *testing.T, pool *pgxpool.Pool, key string) seedEventCoords {
	t.Helper()
	var e seedEventCoords
	var displayLat, displayLon *float64
	err := pool.QueryRow(ctx, `
		SELECT id, ST_Y(geom)::float8, ST_X(geom)::float8,
			ST_Y(display_geom)::float8, ST_X(display_geom)::float8
		FROM public.events
		WHERE seed_key = $1
	`, key).Scan(&e.id, &e.centerLat, &e.centerLon, &displayLat, &displayLon)
	if err != nil {
		t.Fatalf("find event %s: %v", key, err)
	}
	if displayLat != nil {
		e.displayLat = *displayLat
	}
	if displayLon != nil {
		e.displayLon = *displayLon
	}
	return e
}

func pickByID(events []nearbyEvent, id string) *nearbyEvent {
	for i := range events {
		if events[i].ID == id {
			return &events[i]
		}
	}
	return nil
}

func requireSeedEvents(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.events WHERE source = 'curated'`,
	).Scan(&n); err != nil {
		t.Fatalf("count curated: %v", err)
	}
	if n < 3 {
		t.Fatalf("need ≥3 curated events; run `make seed` first (got %d)", n)
	}
}

// insertWindowSeedEvent creates a curated event with start/end offsets
// relative to now, returning its id. Useful for time-filter tests.
func insertWindowSeedEvent(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	startOffset, endOffset time.Duration,
) string {
	t.Helper()
	var hostID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&hostID); err != nil {
		t.Fatalf("find spur-seed host: %v", err)
	}
	now := time.Now().UTC()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'window event', 'window event', 'Other',
			$2, $3, 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID, now.Add(startOffset), now.Add(endOffset)).Scan(&id)
	if err != nil {
		t.Fatalf("insert window event: %v", err)
	}
	return id
}

// insertPastSeedEvent creates a curated event whose end_time is already in
// the past, returning its id. Reuses the spur-seed host so the FK to
// public.users is satisfied.
func insertPastSeedEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var hostID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&hostID); err != nil {
		t.Fatalf("find spur-seed host: %v", err)
	}
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'past event', 'past event', 'Other',
			now() - interval '2 hours', now() - interval '90 minutes', 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID).Scan(&id)
	if err != nil {
		t.Fatalf("insert past event: %v", err)
	}
	return id
}

// ensureProfile lands a complete public.users row for an auth.users-only
// user. ADR 0025 dropped the auth-mirror trigger; tests that don't go
// through POST /users/me/profile upsert directly. Same shape as the
// helpers in friends_test.go, checkins_test.go, and commits_test.go.
func ensureProfile(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID, handle, handleDisplay, displayName string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.users (id, handle, handle_display, display_name, dob, tos_accepted_at, tos_version)
		VALUES ($1, $2, $3, $4, '1990-01-01', now(), 'v1')
		ON CONFLICT (id) DO UPDATE SET
			handle         = EXCLUDED.handle,
			handle_display = EXCLUDED.handle_display,
			display_name   = EXCLUDED.display_name
	`, userID, handle, handleDisplay, displayName); err != nil {
		t.Fatalf("ensure profile: %v", err)
	}
}

// ensureTestUser idempotently creates the events-test user via Admin API and
// returns its UUID. Uses email+password (not phone OTP) so it doesn't collide
// with the OTP-based auth test in internal/auth.
func ensureTestUser(t *testing.T, supabaseURL, serviceKey string) string {
	t.Helper()

	if id, found := adminFindUserByEmail(t, supabaseURL, serviceKey, testEmail); found {
		return id
	}

	body, _ := json.Marshal(map[string]any{
		"email":         testEmail,
		"password":      testPassword,
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

func signInWithPassword(t *testing.T, supabaseURL, anonKey string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    testEmail,
		"password": testPassword,
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
