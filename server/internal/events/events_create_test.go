package events_test

import (
	"bytes"
	"context"
	"encoding/json"
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

type createdEvent struct {
	ID                 string     `json:"id"`
	HostID             string     `json:"host_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Category           string     `json:"category"`
	StartTime          time.Time  `json:"start_time"`
	EndTime            time.Time  `json:"end_time"`
	Cap                *int       `json:"cap"`
	Lat                float64    `json:"lat"`
	Lon                float64    `json:"lon"`
	FuzzRadiusM        int        `json:"fuzz_radius_m"`
	LocationVisibility string     `json:"location_visibility"`
	TipThreshold       *int       `json:"tip_threshold,omitempty"`
	TipDeadline        *time.Time `json:"tip_deadline,omitempty"`
}

func TestCreateEventEndpoint(t *testing.T) {
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

	hostID := ensureTestUser(t, supabaseURL, serviceKey)
	token := signInWithPassword(t, supabaseURL, anonKey)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Post("/events", events.New(pool).Create)
	})

	post := func(t *testing.T, payload map[string]any, withAuth bool) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if withAuth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	now := time.Now().UTC()
	validBody := func() map[string]any {
		return map[string]any{
			"title":               "Test α-Event",
			"description":         "integration test",
			"category":            "Sports",
			"start_time":          now.Add(2 * time.Hour).Format(time.RFC3339Nano),
			"end_time":            now.Add(3 * time.Hour).Format(time.RFC3339Nano),
			"lat":                 34.0522,
			"lon":                 -118.2437,
			"cap":                 10,
			"location_visibility": "fuzzed",
		}
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		rec := post(t, validBody(), false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("past start_time returns 400", func(t *testing.T) {
		body := validBody()
		body["start_time"] = now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("start_time beyond 72hr returns 400", func(t *testing.T) {
		body := validBody()
		body["start_time"] = now.Add(73 * time.Hour).Format(time.RFC3339Nano)
		body["end_time"] = now.Add(74 * time.Hour).Format(time.RFC3339Nano)
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("end_time not after start_time returns 400", func(t *testing.T) {
		body := validBody()
		body["end_time"] = body["start_time"]
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid category returns 400", func(t *testing.T) {
		body := validBody()
		body["category"] = "Volcanology"
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("happy path returns 201 with display_geom inside fuzz_radius", func(t *testing.T) {
		rec := post(t, validBody(), true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		if got.HostID != hostID {
			t.Errorf("host_id mismatch: got %s, want %s", got.HostID, hostID)
		}
		if got.Lat != 34.0522 || got.Lon != -118.2437 {
			t.Errorf("response coords drifted: got (%f, %f), want (34.0522, -118.2437)", got.Lat, got.Lon)
		}
		if got.FuzzRadiusM != 200 {
			t.Errorf("fuzz_radius_m: got %d, want 200", got.FuzzRadiusM)
		}

		var distM float64
		var hasDisplay bool
		err := pool.QueryRow(ctx, `
			SELECT
				display_geom IS NOT NULL,
				COALESCE(ST_DistanceSphere(display_geom, geom), 0)
			FROM public.events WHERE id = $1
		`, got.ID).Scan(&hasDisplay, &distM)
		if err != nil {
			t.Fatalf("query stored row: %v", err)
		}
		if !hasDisplay {
			t.Errorf("fuzzed event must have display_geom set")
		}
		if distM > float64(got.FuzzRadiusM) {
			t.Errorf("display_geom %.1fm from geom; expected ≤ %dm", distM, got.FuzzRadiusM)
		}
	})

	t.Run("public visibility leaves display_geom NULL", func(t *testing.T) {
		body := validBody()
		body["location_visibility"] = "public"
		rec := post(t, body, true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		var hasDisplay bool
		if err := pool.QueryRow(ctx,
			`SELECT display_geom IS NOT NULL FROM public.events WHERE id = $1`, got.ID,
		).Scan(&hasDisplay); err != nil {
			t.Fatalf("query stored row: %v", err)
		}
		if hasDisplay {
			t.Errorf("public event should not store a display_geom")
		}
	})

	t.Run("nullable cap accepted", func(t *testing.T) {
		body := validBody()
		body["cap"] = nil
		rec := post(t, body, true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		if got.Cap != nil {
			t.Errorf("expected null cap, got %v", *got.Cap)
		}
	})

	// β-Event creation per #32. Pair invariant, threshold floor, cap relation,
	// deadline temporal bounds.
	betaBody := func() map[string]any {
		b := validBody()
		b["title"] = "Test β-Event"
		b["tip_threshold"] = 4
		b["tip_deadline"] = now.Add(45 * time.Minute).Format(time.RFC3339Nano)
		return b
	}

	t.Run("β happy path returns 201 with tip fields echoed", func(t *testing.T) {
		rec := post(t, betaBody(), true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		if got.TipThreshold == nil || *got.TipThreshold != 4 {
			t.Errorf("tip_threshold: got %v, want 4", got.TipThreshold)
		}
		if got.TipDeadline == nil {
			t.Errorf("tip_deadline missing in response")
		}
	})

	t.Run("β with only tip_threshold returns 400", func(t *testing.T) {
		body := betaBody()
		delete(body, "tip_deadline")
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("β with only tip_deadline returns 400", func(t *testing.T) {
		body := betaBody()
		delete(body, "tip_threshold")
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("β with tip_threshold < 2 returns 400", func(t *testing.T) {
		body := betaBody()
		body["tip_threshold"] = 1
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("β with cap < tip_threshold returns 400", func(t *testing.T) {
		body := betaBody()
		body["cap"] = 3
		body["tip_threshold"] = 5
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("β with tip_deadline in the past returns 400", func(t *testing.T) {
		body := betaBody()
		body["tip_deadline"] = now.Add(-1 * time.Minute).Format(time.RFC3339Nano)
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("β with tip_deadline within 15min of start returns 400", func(t *testing.T) {
		body := betaBody()
		// start_time is now+2h; deadline at start-10min violates the 15min margin.
		body["tip_deadline"] = now.Add(2*time.Hour - 10*time.Minute).Format(time.RFC3339Nano)
		rec := post(t, body, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	// Seeder auto-commit: a β creator is auto-Committed in the same transaction
	// as the INSERT and counts toward tip_threshold. α creators are not.
	t.Run("β create auto-commits the Seeder", func(t *testing.T) {
		rec := post(t, betaBody(), true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		var hasCommit bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM public.commits WHERE event_id = $1 AND user_id = $2)`,
			got.ID, got.HostID,
		).Scan(&hasCommit); err != nil {
			t.Fatalf("query commit: %v", err)
		}
		if !hasCommit {
			t.Errorf("expected Seeder commits row to exist for β event")
		}
	})

	t.Run("α create does NOT auto-commit the Host", func(t *testing.T) {
		rec := post(t, validBody(), true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var got createdEvent
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, got.ID)
		})
		var hasCommit bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM public.commits WHERE event_id = $1 AND user_id = $2)`,
			got.ID, got.HostID,
		).Scan(&hasCommit); err != nil {
			t.Fatalf("query commit: %v", err)
		}
		if hasCommit {
			t.Errorf("α create must not auto-commit the Host")
		}
	})
}
