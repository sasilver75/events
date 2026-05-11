package reputation_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/reputation"
	"github.com/sasilver75/events/server/internal/testsupport"
)

// TestRecompute drives the formula end-to-end against a real Postgres.
// The math comes from ADR 0008 §Concrete formula; the tests pin the
// observable score, not internal implementation details.
//
// A fresh user (no outcomes, no flags) gets the prior posterior:
// α=4, β=1, behavioral = 4/5 × 100 = 80; flag_factor = 1.0; score = 80.
func TestRecompute(t *testing.T) {
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

	insertEvent := func(t *testing.T) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, source, location_visibility
			) VALUES (
				$1, 'rep test', 'rep test', 'Other',
				now() - interval '2 hours', now() - interval '1 hour', 8,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'curated', 'public'
			) RETURNING id
		`, hostID).Scan(&id)
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.flags WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.feedback_signals WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.attendance_outcomes WHERE event_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
		})
		return id
	}

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
		// public.users no longer auto-mirrored from auth.users (ADR 0025);
		// insert the profile row directly so FKs resolve.
		testsupport.EnsureProfile(t, pool, id, "RepTestUser")
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.reputation WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, id)
		})
		return id
	}

	insertOutcome := func(t *testing.T, eventID, userID, outcome string) {
		t.Helper()
		_, err := pool.Exec(ctx,
			`INSERT INTO public.attendance_outcomes (event_id, user_id, outcome) VALUES ($1, $2, $3)`,
			eventID, userID, outcome,
		)
		if err != nil {
			t.Fatalf("insert outcome: %v", err)
		}
	}

	insertFlag := func(t *testing.T, eventID, voterID, targetID string, reasons ...string) {
		t.Helper()
		_, err := pool.Exec(ctx,
			`INSERT INTO public.flags (event_id, voter_id, target_user_id, reasons) VALUES ($1, $2, $3, $4)`,
			eventID, voterID, targetID, reasons,
		)
		if err != nil {
			t.Fatalf("insert flag: %v", err)
		}
	}

	readRep := func(t *testing.T, userID string) (score int, count int, present bool) {
		t.Helper()
		err := pool.QueryRow(ctx,
			`SELECT attendee_score, attendee_event_count FROM public.reputation WHERE user_id = $1`,
			userID,
		).Scan(&score, &count)
		if err != nil {
			return 0, 0, false
		}
		return score, count, true
	}

	t.Run("fresh user with no outcomes scores at the prior (80)", func(t *testing.T) {
		uid := insertUser(t)
		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute: %v", err)
		}
		score, count, ok := readRep(t, uid)
		if !ok {
			t.Fatalf("reputation row not written")
		}
		if score != 80 {
			t.Errorf("attendee_score: got %d, want 80 (Beta(4,1) prior)", score)
		}
		if count != 0 {
			t.Errorf("attendee_event_count: got %d, want 0", count)
		}
	})

	t.Run("Show outcome lifts score above prior", func(t *testing.T) {
		eventID := insertEvent(t)
		uid := insertUser(t)
		insertOutcome(t, eventID, uid, "show")

		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute: %v", err)
		}
		score, count, ok := readRep(t, uid)
		if !ok {
			t.Fatalf("reputation row not written")
		}
		if score < 80 {
			t.Errorf("attendee_score with one Show: got %d, want > 80", score)
		}
		if count != 1 {
			t.Errorf("attendee_event_count: got %d, want 1", count)
		}
	})

	t.Run("Ghost outcome drops score sharply (3× failure weight)", func(t *testing.T) {
		eventID := insertEvent(t)
		uid := insertUser(t)
		insertOutcome(t, eventID, uid, "ghost")

		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute: %v", err)
		}
		score, _, _ := readRep(t, uid)
		// α=4, β=1+3=4 → 4/8 = 50.
		if score < 45 || score > 55 {
			t.Errorf("attendee_score with one Ghost: got %d, want ~50", score)
		}
	})

	t.Run("hard flag depresses score via flag_factor", func(t *testing.T) {
		eventID := insertEvent(t)
		uid := insertUser(t)
		voter := insertUser(t)
		insertOutcome(t, eventID, uid, "show")
		insertOutcome(t, eventID, voter, "show")

		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute pre-flag: %v", err)
		}
		preFlag, _, _ := readRep(t, uid)

		insertFlag(t, eventID, voter, uid, "concerning_behavior")
		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute post-flag: %v", err)
		}
		postFlag, _, _ := readRep(t, uid)
		if postFlag >= preFlag {
			t.Errorf("flag should depress score: pre=%d post=%d", preFlag, postFlag)
		}
		// One flag → flag_factor ≈ 0.88; ratio should be in that ballpark.
		ratio := float64(postFlag) / float64(preFlag)
		if ratio < 0.85 || ratio > 0.92 {
			t.Errorf("post/pre ratio: got %.3f, want ≈ 0.88", ratio)
		}
	})

	t.Run("flag_factor floors at 0.4 — coordinated harassment can't zero out the score", func(t *testing.T) {
		eventID := insertEvent(t)
		uid := insertUser(t)
		insertOutcome(t, eventID, uid, "show")

		// Pile on flags well past the linear-saturation point. Each flag
		// from a different voter row.
		for i := 0; i < 20; i++ {
			voter := insertUser(t)
			insertFlag(t, eventID, voter, uid, "concerning_behavior")
		}

		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute: %v", err)
		}
		score, _, _ := readRep(t, uid)
		// behavioral with 1 Show is 5/6 × 100 ≈ 83; floor 0.4 → ≥ 33.
		if score < 30 || score > 36 {
			t.Errorf("score with 20 flags: got %d, want ≈ 33 (floor)", score)
		}
	})

	t.Run("idempotent — repeat Recompute lands the same score", func(t *testing.T) {
		eventID := insertEvent(t)
		uid := insertUser(t)
		insertOutcome(t, eventID, uid, "show")

		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute 1: %v", err)
		}
		first, _, _ := readRep(t, uid)
		if err := reputation.Recompute(ctx, pool, uid); err != nil {
			t.Fatalf("Recompute 2: %v", err)
		}
		second, _, _ := readRep(t, uid)
		if first != second {
			t.Errorf("non-idempotent: first=%d second=%d", first, second)
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
