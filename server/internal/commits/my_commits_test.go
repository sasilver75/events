package commits_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/auth"
	"github.com/sasilver75/events/server/internal/commits"
	"github.com/sasilver75/events/server/internal/testsupport"
)

const (
	myCommitsEmail    = "my-commits-test@spur.local"
	myCommitsPassword = "my-commits-test-not-secret"
	myCommitsOther    = "my-commits-other@spur.local"
	myCommitsOtherPwd = "my-commits-other-not-secret"
)

type myCommitsEventRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Category  string    `json:"category"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	State     string    `json:"state"`
}

type myCommitsBody struct {
	Upcoming []myCommitsEventRow `json:"upcoming"`
	Recent   []myCommitsEventRow `json:"recent"`
}

func TestMyCommitsEndpoint(t *testing.T) {
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
	// Register the pool's close via t.Cleanup (not defer) so it runs LAST
	// in LIFO order — fixture-deletion cleanups registered below need a live
	// pool, and t.Cleanup callbacks fire after the test function's defers.
	t.Cleanup(pool.Close)

	userID := ensureTestUser(t, supabaseURL, serviceKey, myCommitsEmail, myCommitsPassword)
	otherUserID := ensureTestUser(t, supabaseURL, serviceKey, myCommitsOther, myCommitsOtherPwd)
	testsupport.EnsureProfile(t, pool, userID, "MyCommitsTester")
	testsupport.EnsureProfile(t, pool, otherUserID, "MyCommitsOther")
	token := signInWithPassword(t, supabaseURL, anonKey, myCommitsEmail, myCommitsPassword)

	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	h := commits.New(pool)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/users/me/commits", h.MyCommits)
	})

	hostID := seedHostID(ctx, t, pool)

	// Insert fixture Events covering every bucket the endpoint partitions on.
	// Times are anchored to the request's perception of now() (Go side) — the
	// SQL function uses transaction-local now(); a few seconds skew between
	// the two is harmless as long as fixtures stay well clear of boundaries.
	upcomingID := insertEventAt(ctx, t, pool, hostID, "upcoming α",
		time.Now().Add(2*time.Hour), time.Now().Add(3*time.Hour))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, upcomingID) })

	liveID := insertEventAt(ctx, t, pool, hostID, "live α",
		time.Now().Add(-30*time.Minute), time.Now().Add(30*time.Minute))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, liveID) })

	recentID := insertEventAt(ctx, t, pool, hostID, "recent α",
		time.Now().Add(-3*24*time.Hour), time.Now().Add(-3*24*time.Hour).Add(time.Hour))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, recentID) })

	oldDoneID := insertEventAt(ctx, t, pool, hostID, "old α",
		time.Now().Add(-30*24*time.Hour), time.Now().Add(-30*24*time.Hour).Add(time.Hour))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, oldDoneID) })

	withdrawnID := insertEventAt(ctx, t, pool, hostID, "withdrawn α",
		time.Now().Add(4*time.Hour), time.Now().Add(5*time.Hour))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, withdrawnID) })

	otherUserEventID := insertEventAt(ctx, t, pool, hostID, "other user only",
		time.Now().Add(1*time.Hour), time.Now().Add(2*time.Hour))
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, otherUserEventID) })

	// β-Event whose threshold has been reached pre-start: event_state() reads
	// "Tipped" until start_time arrives. Setting tipped_at directly avoids
	// having to thread the threshold-reaching Commit through the test, which
	// is the Commit handler's responsibility (covered in TestCommitsEndpoints).
	tippedID := insertBetaEventAt(ctx, t, pool, hostID, "tipped β",
		time.Now().Add(5*time.Hour), time.Now().Add(6*time.Hour),
		time.Now().Add(1*time.Hour), 2)
	if _, err := pool.Exec(ctx,
		`UPDATE public.events SET tipped_at = now() WHERE id = $1`, tippedID,
	); err != nil {
		t.Fatalf("force tipped_at: %v", err)
	}
	t.Cleanup(func() { clearAndDelete(ctx, t, pool, tippedID) })

	// Seed commits: the caller commits to upcoming, live, recent, oldDone.
	// withdrawnID is committed-then-deleted to mirror a real Withdraw. Other
	// user's event has only otherUserID committed — the caller must not see
	// it in their projection.
	for _, eid := range []string{upcomingID, liveID, recentID, oldDoneID, tippedID} {
		insertCommit(ctx, t, pool, eid, userID)
	}
	insertCommit(ctx, t, pool, withdrawnID, userID)
	deleteCommit(ctx, t, pool, withdrawnID, userID)
	insertCommit(ctx, t, pool, otherUserEventID, otherUserID)

	doRequest := func(t *testing.T, token string) (*httptest.ResponseRecorder, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/users/me/commits", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec, body
	}

	decode := func(t *testing.T, body []byte) myCommitsBody {
		t.Helper()
		var got myCommitsBody
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode response: %v: %s", err, body)
		}
		return got
	}

	t.Run("missing token returns 401", func(t *testing.T) {
		rec, _ := doRequest(t, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("partitions upcoming vs recent and excludes withdrawn/old/other", func(t *testing.T) {
		rec, body := doRequest(t, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, body)
		}
		got := decode(t, body)

		upcomingIDs := idsOf(got.Upcoming)
		recentIDs := idsOf(got.Recent)
		all := append(append([]string{}, upcomingIDs...), recentIDs...)

		if !containsID(upcomingIDs, upcomingID) {
			t.Errorf("upcoming missing scheduled event: %v", upcomingIDs)
		}
		if !containsID(upcomingIDs, liveID) {
			t.Errorf("upcoming missing live event: %v", upcomingIDs)
		}
		if !containsID(recentIDs, recentID) {
			t.Errorf("recent missing 3-day-old Done event: %v", recentIDs)
		}
		if containsID(all, oldDoneID) {
			t.Errorf("30-day-old Done event should not appear: %v", all)
		}
		if containsID(all, withdrawnID) {
			t.Errorf("withdrawn commit should not appear: %v", all)
		}
		if containsID(all, otherUserEventID) {
			t.Errorf("other user's event should not appear: %v", all)
		}
	})

	t.Run("lifecycle state reflects event_state", func(t *testing.T) {
		_, body := doRequest(t, token)
		got := decode(t, body)

		stateByID := map[string]string{}
		for _, e := range got.Upcoming {
			stateByID[e.ID] = e.State
		}
		for _, e := range got.Recent {
			stateByID[e.ID] = e.State
		}

		// upcoming α with the caller's Commit but no tip_threshold lives in
		// Filling. liveID is post-start/pre-end → Live. recentID is post-end
		// → Done.
		if got := stateByID[upcomingID]; got != "Filling" {
			t.Errorf("upcoming state: got %q, want Filling", got)
		}
		if got := stateByID[liveID]; got != "Live" {
			t.Errorf("live state: got %q, want Live", got)
		}
		if got := stateByID[recentID]; got != "Done" {
			t.Errorf("recent state: got %q, want Done", got)
		}
		if got := stateByID[tippedID]; got != "Tipped" {
			t.Errorf("tipped state: got %q, want Tipped", got)
		}
	})

	t.Run("upcoming sorted ascending by start_time", func(t *testing.T) {
		_, body := doRequest(t, token)
		got := decode(t, body)
		for i := 1; i < len(got.Upcoming); i++ {
			if got.Upcoming[i].StartTime.Before(got.Upcoming[i-1].StartTime) {
				t.Errorf("upcoming not ascending: %v then %v",
					got.Upcoming[i-1].StartTime, got.Upcoming[i].StartTime)
			}
		}
	})
}

func idsOf(rows []myCommitsEventRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func containsID(ids []string, id string) bool {
	return slices.Contains(ids, id)
}

// insertEventAt is a thin wrapper around the shared helper in commits_test.go
// that lets the test fix start_time / end_time explicitly. The shared helper
// hardcodes a "+1h / +2h" window which doesn't reach the recent / old-Done /
// live buckets this test needs to exercise.
func insertEventAt(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID, title string, start, end time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, $2, 'my commits test', 'Other',
			$3, $4, 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID, title, start, end).Scan(&id)
	if err != nil {
		t.Fatalf("insert event %q: %v", title, err)
	}
	return id
}

func insertBetaEventAt(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostID, title string, start, end, tipDeadline time.Time, tipThreshold int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility,
			tip_threshold, tip_deadline
		) VALUES (
			$1, $2, 'my commits β test', 'Other',
			$3, $4, 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public',
			$5, $6
		) RETURNING id
	`, hostID, title, start, end, tipThreshold, tipDeadline).Scan(&id)
	if err != nil {
		t.Fatalf("insert β event %q: %v", title, err)
	}
	return id
}

func insertCommit(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID, userID string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
		eventID, userID,
	); err != nil {
		t.Fatalf("insert commit: %v", err)
	}
}

func deleteCommit(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID, userID string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`DELETE FROM public.commits WHERE event_id = $1 AND user_id = $2`,
		eventID, userID,
	); err != nil {
		t.Fatalf("delete commit: %v", err)
	}
}

func clearAndDelete(ctx context.Context, t *testing.T, pool *pgxpool.Pool, eventID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM public.commits WHERE event_id = $1`, eventID); err != nil {
		t.Errorf("clear commits: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, eventID); err != nil {
		t.Errorf("delete event: %v", err)
	}
}
