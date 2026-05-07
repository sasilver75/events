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
