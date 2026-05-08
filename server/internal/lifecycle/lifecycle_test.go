package lifecycle_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/lifecycle"
)

// TestCancelExpiredBetaEvents drives the auto-cancel work directly (no
// ticker). The Run loop is just orchestration and is exercised implicitly
// by the in-process server during the manual sim test.
func TestCancelExpiredBetaEvents(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; integration test requires a real Postgres")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	hostID := requireSeedHost(ctx, t, pool)
	runner := lifecycle.New(pool)

	insert := func(t *testing.T, tipDeadline time.Time, tipped, cancelled bool, tipThreshold *int) string {
		t.Helper()
		var tippedAt, cancelledAt *time.Time
		if tipped {
			now := time.Now()
			tippedAt = &now
		}
		if cancelled {
			now := time.Now().Add(-5 * time.Minute)
			cancelledAt = &now
		}
		var thr *int
		if tipThreshold != nil {
			thr = tipThreshold
		}
		var deadline *time.Time
		if !tipDeadline.IsZero() {
			deadline = &tipDeadline
		}
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility,
				tip_threshold, tip_deadline, tipped_at, cancelled_at
			) VALUES (
				$1, 'lifecycle test', 'lifecycle test', 'Other',
				now() + interval '4 hours', now() + interval '5 hours', 4,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public',
				$2, $3, $4, $5
			) RETURNING id
		`, hostID, thr, deadline, tippedAt, cancelledAt).Scan(&id)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id) })
		return id
	}

	cancelledAtOf := func(t *testing.T, id string) *time.Time {
		t.Helper()
		var ts *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT cancelled_at FROM public.events WHERE id = $1`, id,
		).Scan(&ts); err != nil {
			t.Fatalf("read cancelled_at: %v", err)
		}
		return ts
	}

	thr := 3

	t.Run("β past deadline + not tipped → cancelled_at set", func(t *testing.T) {
		id := insert(t, time.Now().Add(-1*time.Minute), false, false, &thr)
		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if cancelledAtOf(t, id) == nil {
			t.Errorf("expected cancelled_at set; got NULL")
		}
	})

	t.Run("β past deadline + already tipped → untouched", func(t *testing.T) {
		id := insert(t, time.Now().Add(-1*time.Minute), true, false, &thr)
		_ = runner.RunOnce(ctx)
		if cancelledAtOf(t, id) != nil {
			t.Errorf("tipped β must not be auto-cancelled")
		}
	})

	t.Run("β before deadline → untouched", func(t *testing.T) {
		id := insert(t, time.Now().Add(1*time.Hour), false, false, &thr)
		_ = runner.RunOnce(ctx)
		if cancelledAtOf(t, id) != nil {
			t.Errorf("β before deadline must not be auto-cancelled")
		}
	})

	t.Run("β already cancelled → cancelled_at not bumped", func(t *testing.T) {
		id := insert(t, time.Now().Add(-1*time.Minute), false, true, &thr)
		before := cancelledAtOf(t, id)
		_ = runner.RunOnce(ctx)
		after := cancelledAtOf(t, id)
		if before == nil || after == nil || !before.Equal(*after) {
			t.Errorf("cancelled_at changed: before=%v after=%v", before, after)
		}
	})

	t.Run("α-Event ignored entirely (no tip_threshold)", func(t *testing.T) {
		id := insert(t, time.Time{}, false, false, nil)
		_ = runner.RunOnce(ctx)
		if cancelledAtOf(t, id) != nil {
			t.Errorf("α-Event must never be auto-cancelled by the β loop")
		}
	})
}

// TestResolveDoneEventOutcomes drives Done-state outcome resolution
// directly via Runner.RunOnce. Real Postgres only — outcome resolution is
// a SQL-shape concern and a mock would let regressions through.
func TestResolveDoneEventOutcomes(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; integration test requires a real Postgres")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	hostID := requireSeedHost(ctx, t, pool)
	runner := lifecycle.New(pool)

	// insertEvent writes an α-Event with the requested time window. Pass
	// negative durations to land the event in the past; positive to keep it
	// future. Returns the event id and registers cleanup.
	insertEvent := func(t *testing.T, startsIn, endsIn time.Duration, cancelled bool) string {
		t.Helper()
		var cancelledAt *time.Time
		if cancelled {
			c := time.Now().Add(-1 * time.Minute)
			cancelledAt = &c
		}
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility,
				cancelled_at
			) VALUES (
				$1, 'done-poller test', 'done-poller test', 'Other',
				now() + make_interval(secs => $2), now() + make_interval(secs => $3), 8,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public',
				$4
			) RETURNING id
		`, hostID, startsIn.Seconds(), endsIn.Seconds(), cancelledAt).Scan(&id)
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.attendance_outcomes WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.checkins WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.commits WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
		})
		return id
	}

	// insertUser creates an auth.users row; the mirror trigger from
	// migration 0006 propagates a matching public.users row.
	insertUser := func(t *testing.T) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO auth.users (id, email)
			VALUES (gen_random_uuid(), gen_random_uuid()::text || '@test.local')
			RETURNING id
		`).Scan(&id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, id)
		})
		return id
	}

	commit := func(t *testing.T, eventID, userID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
			eventID, userID,
		); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	checkin := func(t *testing.T, eventID, userID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.checkins (event_id, user_id) VALUES ($1, $2)`,
			eventID, userID,
		); err != nil {
			t.Fatalf("checkin: %v", err)
		}
	}

	outcomeOf := func(t *testing.T, eventID, userID string) (string, bool) {
		t.Helper()
		var outcome string
		err := pool.QueryRow(ctx,
			`SELECT outcome FROM public.attendance_outcomes WHERE event_id = $1 AND user_id = $2`,
			eventID, userID,
		).Scan(&outcome)
		if err != nil {
			return "", false
		}
		return outcome, true
	}

	countOutcomes := func(t *testing.T, eventID string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.attendance_outcomes WHERE event_id = $1`, eventID,
		).Scan(&n); err != nil {
			t.Fatalf("count outcomes: %v", err)
		}
		return n
	}

	t.Run("Done event resolves Show for checked-in Attendee, Ghost for the rest", func(t *testing.T) {
		eventID := insertEvent(t, -2*time.Hour, -1*time.Hour, false)
		showUser := insertUser(t)
		ghostUser := insertUser(t)
		commit(t, eventID, showUser)
		commit(t, eventID, ghostUser)
		checkin(t, eventID, showUser)

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		if got, ok := outcomeOf(t, eventID, showUser); !ok || got != "show" {
			t.Errorf("checked-in Attendee: want 'show', got %q (present=%v)", got, ok)
		}
		if got, ok := outcomeOf(t, eventID, ghostUser); !ok || got != "ghost" {
			t.Errorf("no-checkin Attendee: want 'ghost', got %q (present=%v)", got, ok)
		}
		if n := countOutcomes(t, eventID); n != 2 {
			t.Errorf("expected exactly 2 outcome rows, got %d", n)
		}
	})

	t.Run("Idempotent on rerun", func(t *testing.T) {
		eventID := insertEvent(t, -2*time.Hour, -1*time.Hour, false)
		userID := insertUser(t)
		commit(t, eventID, userID)
		checkin(t, eventID, userID)

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce 1: %v", err)
		}
		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce 2: %v", err)
		}
		if n := countOutcomes(t, eventID); n != 1 {
			t.Errorf("expected 1 outcome row after two ticks, got %d", n)
		}
	})

	t.Run("Cancelled Event past end_time produces no outcome rows", func(t *testing.T) {
		eventID := insertEvent(t, -2*time.Hour, -1*time.Hour, true)
		userID := insertUser(t)
		commit(t, eventID, userID)

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n := countOutcomes(t, eventID); n != 0 {
			t.Errorf("Cancelled Event must produce 0 outcomes, got %d", n)
		}
	})

	t.Run("Future Event produces no outcome rows", func(t *testing.T) {
		eventID := insertEvent(t, 1*time.Hour, 2*time.Hour, false)
		userID := insertUser(t)
		commit(t, eventID, userID)

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n := countOutcomes(t, eventID); n != 0 {
			t.Errorf("future Event must produce 0 outcomes, got %d", n)
		}
	})

	t.Run("Live Event (started but not ended) produces no outcome rows", func(t *testing.T) {
		eventID := insertEvent(t, -30*time.Minute, 30*time.Minute, false)
		userID := insertUser(t)
		commit(t, eventID, userID)
		checkin(t, eventID, userID)

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n := countOutcomes(t, eventID); n != 0 {
			t.Errorf("Live Event must produce 0 outcomes, got %d", n)
		}
	})

	t.Run("Withdrawn Attendee (no commits row) produces no outcome row", func(t *testing.T) {
		eventID := insertEvent(t, -2*time.Hour, -1*time.Hour, false)
		stayed := insertUser(t)
		withdrew := insertUser(t)
		commit(t, eventID, stayed)
		commit(t, eventID, withdrew)
		// Withdraw is a hard DELETE per commits.Withdraw.
		if _, err := pool.Exec(ctx,
			`DELETE FROM public.commits WHERE event_id = $1 AND user_id = $2`,
			eventID, withdrew,
		); err != nil {
			t.Fatalf("simulate Withdraw: %v", err)
		}

		if err := runner.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		if _, ok := outcomeOf(t, eventID, withdrew); ok {
			t.Errorf("withdrawn Attendee must not have an outcome row")
		}
		if got, ok := outcomeOf(t, eventID, stayed); !ok || got != "ghost" {
			t.Errorf("remaining Attendee without checkin: want 'ghost', got %q (present=%v)", got, ok)
		}
		if n := countOutcomes(t, eventID); n != 1 {
			t.Errorf("expected 1 outcome row, got %d", n)
		}
	})
}

// TestDoneFanOutToReputation verifies that when the Done poller writes
// outcome rows for a fresh user, that user gets a reputation row in the
// same tick (PRD §Reputation system §Recompute cadence).
func TestDoneFanOutToReputation(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; integration test requires a real Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	hostID := requireSeedHost(ctx, t, pool)
	runner := lifecycle.New(pool)

	var eventID string
	err = pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, source, location_visibility
		) VALUES (
			$1, 'rep-fanout test', 'rep-fanout test', 'Other',
			now() - interval '2 hours', now() - interval '1 hour', 8,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'curated', 'public'
		) RETURNING id
	`, hostID).Scan(&eventID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.attendance_outcomes WHERE event_id = $1`, eventID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.commits WHERE event_id = $1`, eventID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, eventID)
	})

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth.users (id, email)
		VALUES (gen_random_uuid(), gen_random_uuid()::text || '@test.local')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.reputation WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
		eventID, userID,
	); err != nil {
		t.Fatalf("insert commit: %v", err)
	}

	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var score int
	if err := pool.QueryRow(ctx,
		`SELECT attendee_score FROM public.reputation WHERE user_id = $1`, userID,
	).Scan(&score); err != nil {
		t.Fatalf("expected reputation row after Done fan-out: %v", err)
	}
	// One Ghost outcome → α=4, β=4 → 50.
	if score < 45 || score > 55 {
		t.Errorf("score after Ghost fan-out: got %d, want ~50", score)
	}
}

// TestRefreshStaleReputations verifies the daily-cadence sweep: a row
// whose last_recomputed_at is older than 24hr is recomputed; a fresh row
// is left alone.
func TestRefreshStaleReputations(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; integration test requires a real Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	runner := lifecycle.New(pool)

	mkUser := func(t *testing.T) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO auth.users (id, email)
			VALUES (gen_random_uuid(), gen_random_uuid()::text || '@test.local')
			RETURNING id
		`).Scan(&id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.reputation WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, id)
		})
		return id
	}

	staleUser := mkUser(t)
	freshUser := mkUser(t)

	// Seed both as if RecomputeReputation had run before — but with
	// last_recomputed_at far in the past for stale, recent for fresh.
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.reputation (user_id, attendee_score, last_recomputed_at)
		VALUES ($1, 50, now() - interval '48 hours'),
		       ($2, 50, now() - interval '1 hour')
	`, staleUser, freshUser); err != nil {
		t.Fatalf("seed reputation: %v", err)
	}

	if err := runner.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var staleAt, freshAt time.Time
	_ = pool.QueryRow(ctx,
		`SELECT last_recomputed_at FROM public.reputation WHERE user_id = $1`, staleUser,
	).Scan(&staleAt)
	_ = pool.QueryRow(ctx,
		`SELECT last_recomputed_at FROM public.reputation WHERE user_id = $1`, freshUser,
	).Scan(&freshAt)

	if time.Since(staleAt) > 1*time.Minute {
		t.Errorf("stale reputation row not refreshed: last_recomputed_at = %v", staleAt)
	}
	if time.Since(freshAt) < 30*time.Minute {
		t.Errorf("fresh reputation row should not have been touched, but was: last_recomputed_at = %v", freshAt)
	}
}

func requireSeedHost(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.users WHERE display_name = 'Spur Seed' LIMIT 1`,
	).Scan(&id); err != nil {
		t.Fatalf("find spur-seed host (run `make seed` first): %v", err)
	}
	return id
}
