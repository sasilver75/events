package events_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestEventStateFunction exercises public.event_state(events_row) directly,
// constructing rows in every reachable lifecycle state and asserting the
// function returns the expected string. Per #31 / PRD-v0 §274–278.
func TestEventStateFunction(t *testing.T) {
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

	type tc struct {
		name      string
		startTime time.Time
		endTime   time.Time
		tippedAt  *time.Time
		cancelAt  *time.Time
		// withCommit inserts a commit row from the seed host, so we can prove
		// 'Filling' kicks in when count > 0 pre-start.
		withCommit bool
		want       string
	}

	now := time.Now()
	hour := time.Hour
	min := time.Minute
	tipped := now.Add(-30 * min)
	cancelled := now.Add(-15 * min)

	cases := []tc{
		{
			name:      "Open: future event, no commits, not tipped, not cancelled",
			startTime: now.Add(2 * hour),
			endTime:   now.Add(3 * hour),
			want:      "Open",
		},
		{
			name:       "Filling: future event with at least one commit, not tipped",
			startTime:  now.Add(2 * hour),
			endTime:    now.Add(3 * hour),
			withCommit: true,
			want:       "Filling",
		},
		{
			name:      "Tipped: future β-Event with tipped_at set",
			startTime: now.Add(2 * hour),
			endTime:   now.Add(3 * hour),
			tippedAt:  &tipped,
			want:      "Tipped",
		},
		{
			name:      "Live: now in [start_time, end_time)",
			startTime: now.Add(-30 * min),
			endTime:   now.Add(30 * min),
			want:      "Live",
		},
		{
			name:      "Live: tipped β-Event past start reads Live, not Tipped",
			startTime: now.Add(-30 * min),
			endTime:   now.Add(30 * min),
			tippedAt:  &tipped,
			want:      "Live",
		},
		{
			name:      "Done: end_time in the past",
			startTime: now.Add(-2 * hour),
			endTime:   now.Add(-90 * min),
			want:      "Done",
		},
		{
			name:      "Cancelled wins over Done: cancelled past-end event",
			startTime: now.Add(-2 * hour),
			endTime:   now.Add(-90 * min),
			cancelAt:  &cancelled,
			want:      "Cancelled",
		},
		{
			name:      "Cancelled wins over Live: cancelled in-progress event",
			startTime: now.Add(-30 * min),
			endTime:   now.Add(30 * min),
			cancelAt:  &cancelled,
			want:      "Cancelled",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := insertStateFixtureEvent(ctx, t, pool, hostID, c.startTime, c.endTime, c.tippedAt, c.cancelAt)
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
			})
			if c.withCommit {
				if _, err := pool.Exec(ctx,
					`INSERT INTO public.commits (event_id, user_id) VALUES ($1, $2)`,
					id, hostID,
				); err != nil {
					t.Fatalf("insert commit: %v", err)
				}
			}

			var got string
			err := pool.QueryRow(ctx,
				`SELECT public.event_state(e) FROM public.events e WHERE id = $1`,
				id,
			).Scan(&got)
			if err != nil {
				t.Fatalf("event_state: %v", err)
			}
			if got != c.want {
				t.Errorf("event_state = %q, want %q", got, c.want)
			}
		})
	}

	// Edge case from #31 ACs: an α-Event with start_time = creation_time reads
	// 'Live' immediately — no intermediate 'Open' write. The insert sets
	// start_time from Postgres's own now() so creation_time genuinely matches,
	// dodging Go-vs-Postgres clock skew.
	t.Run("immediate-Live: start_time = now() reads Live with no Open phase", func(t *testing.T) {
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO public.events (
				host_id, title, description, category,
				start_time, end_time, cap,
				geom, location_visibility, source
			) VALUES (
				$1, 'immediate-Live fixture', 'immediate-Live fixture', 'Other',
				now(), now() + interval '1 hour', 4,
				ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
				'public', 'curated'
			) RETURNING id
		`, hostID).Scan(&id)
		if err != nil {
			t.Fatalf("insert immediate-Live fixture: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM public.events WHERE id = $1`, id)
		})

		var got string
		if err := pool.QueryRow(ctx,
			`SELECT public.event_state(e) FROM public.events e WHERE id = $1`,
			id,
		).Scan(&got); err != nil {
			t.Fatalf("event_state: %v", err)
		}
		if got != "Live" {
			t.Errorf("immediate-Live: event_state = %q, want %q", got, "Live")
		}
	})
}

// requireSeedHost returns the spur-seed user id, which #8's seed runner
// provisions as a service-role-owned host. Tests reuse it as the host for
// fixture rows.
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

func insertStateFixtureEvent(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	hostID string,
	startTime, endTime time.Time,
	tippedAt, cancelAt *time.Time,
) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.events (
			host_id, title, description, category,
			start_time, end_time, cap,
			geom, location_visibility, source,
			tipped_at, cancelled_at
		) VALUES (
			$1, 'state fixture', 'state fixture', 'Other',
			$2, $3, 4,
			ST_SetSRID(ST_MakePoint(-118.2437, 34.0522), 4326),
			'public', 'curated',
			$4, $5
		) RETURNING id
	`, hostID, startTime, endTime, tippedAt, cancelAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert fixture event: %v", err)
	}
	return id
}
