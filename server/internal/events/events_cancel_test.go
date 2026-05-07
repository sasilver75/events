package events_test

import (
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
	"github.com/sasilver75/events/server/internal/events"
)

// TestCancelEndpoint exercises DELETE /events/{id} per #32 + ADR 0001:
// β creators get 403 (Seeders cannot cancel); α creators get 501 until
// the α-Host cancel slice lands; non-creators get 403; unknown rows get 404.
func TestCancelEndpoint(t *testing.T) {
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
		r.Delete("/events/{id}", events.New(pool).Cancel)
	})

	doDelete := func(t *testing.T, eventID string, withAuth bool) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/events/"+eventID, nil)
		if withAuth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec, body
	}

	insertAlpha := func(t *testing.T, host string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility
			) VALUES (
				$1, 'cancel test α', 'cancel test α', 'Other',
				now() + interval '2 hours', now() + interval '3 hours', 4,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public'
			) RETURNING id
		`, host).Scan(&id); err != nil {
			t.Fatalf("insert α: %v", err)
		}
		return id
	}
	insertBeta := func(t *testing.T, host string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility,
				tip_threshold, tip_deadline
			) VALUES (
				$1, 'cancel test β', 'cancel test β', 'Other',
				now() + interval '2 hours', now() + interval '3 hours', 4,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public',
				3, now() + interval '1 hour'
			) RETURNING id
		`, host).Scan(&id); err != nil {
			t.Fatalf("insert β: %v", err)
		}
		return id
	}
	cleanup := func(t *testing.T, id string) {
		t.Helper()
		_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
	}

	type errBody struct {
		Error string `json:"error"`
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		eventID := insertAlpha(t, hostID)
		t.Cleanup(func() { cleanup(t, eventID) })
		rec, _ := doDelete(t, eventID, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("unknown event returns 404", func(t *testing.T) {
		const bogus = "00000000-0000-0000-0000-000000000000"
		rec, _ := doDelete(t, bogus, true)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("non-creator returns 403", func(t *testing.T) {
		// Owned by the seed host, not the test user.
		var seedHostID string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
		).Scan(&seedHostID); err != nil {
			t.Fatalf("seed host lookup: %v", err)
		}
		eventID := insertAlpha(t, seedHostID)
		t.Cleanup(func() { cleanup(t, eventID) })
		rec, body := doDelete(t, eventID, true)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
		var er errBody
		_ = json.Unmarshal(body, &er)
		if er.Error != "not the event creator" {
			t.Errorf("error: got %q, want %q", er.Error, "not the event creator")
		}
	})

	t.Run("β creator returns 403 seeders_cannot_cancel per ADR 0001", func(t *testing.T) {
		eventID := insertBeta(t, hostID)
		t.Cleanup(func() { cleanup(t, eventID) })
		rec, body := doDelete(t, eventID, true)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rec.Code, body)
		}
		var er errBody
		_ = json.Unmarshal(body, &er)
		if er.Error != "seeders_cannot_cancel" {
			t.Errorf("error: got %q, want %q", er.Error, "seeders_cannot_cancel")
		}
	})

	t.Run("α creator returns 501 (handler not yet implemented)", func(t *testing.T) {
		eventID := insertAlpha(t, hostID)
		t.Cleanup(func() { cleanup(t, eventID) })
		rec, body := doDelete(t, eventID, true)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("expected 501, got %d body=%s", rec.Code, body)
		}
	})
}
