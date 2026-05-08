// Package lifecycle runs the periodic background work that advances Events
// through their lifecycle independently of any HTTP request: β-Event
// auto-cancel (#32), Done-state outcome resolution (#34), and the
// downstream reputation maintenance that hangs off both — fan-out
// recompute on every newly resolved outcome, plus a daily refresh that
// keeps cached scores within ≤24hr of decay-true (#35, PRD §Reputation
// system).
//
// Single-replica assumption: this loop runs inside the HTTP server process.
// If we ever scale beyond one replica, every replica will run its own loop
// — the per-row work is idempotent (sticky tipped_at, conditional UPDATE
// guarded by IS NULL, RecomputeReputation is pure-SQL upsert), but
// duplicate queries waste cycles. Add pg_try_advisory_lock at the top of
// each tick before scaling out.
package lifecycle

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sasilver75/events/server/internal/reputation"
)

const defaultTickInterval = 30 * time.Second

// reputationStaleThreshold bounds cached-score drift from decay at ≤24hr
// per PRD §Reputation system §Decay refresh for inactive users. The
// per-tick refresh sweeps any reputation rows older than this.
const reputationStaleThreshold = 24 * time.Hour

// reputationStaleBatchSize caps the per-tick refresh sweep so a backlog
// (e.g., the very first tick after this slice ships, when every existing
// user is "stale") spreads across multiple ticks instead of stalling the
// loop. At v0 scale (≤ a few hundred users) this is a nominal limit; the
// next tick picks up where this one left off.
const reputationStaleBatchSize = 100

type Runner struct {
	pool     *pgxpool.Pool
	interval time.Duration
}

func New(pool *pgxpool.Pool) *Runner {
	return &Runner{pool: pool, interval: defaultTickInterval}
}

// Run blocks until ctx is cancelled, ticking every interval. A failed tick
// is logged and the loop continues — a single transient DB blip should not
// stop background lifecycle work. Per-row writes are idempotent so a
// retry on the next tick lands the same end state.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
		log.Printf("lifecycle: tick: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
				log.Printf("lifecycle: tick: %v", err)
			}
		}
	}
}

// RunOnce performs a single iteration of all background work. Public so
// tests can drive the loop synchronously without waiting for the ticker.
//
// A failure in one step does not abort the others — the per-row writes are
// idempotent and the next tick will retry.
func (r *Runner) RunOnce(ctx context.Context) error {
	var firstErr error
	if err := r.cancelExpiredBetaEvents(ctx); err != nil {
		firstErr = err
		log.Printf("lifecycle: cancelExpiredBetaEvents: %v", err)
	}
	if err := r.resolveDoneEventOutcomes(ctx); err != nil {
		if firstErr == nil {
			firstErr = err
		}
		log.Printf("lifecycle: resolveDoneEventOutcomes: %v", err)
	}
	if err := r.refreshStaleReputations(ctx); err != nil {
		if firstErr == nil {
			firstErr = err
		}
		log.Printf("lifecycle: refreshStaleReputations: %v", err)
	}
	return firstErr
}

// cancelExpiredBetaEvents marks any β-Event past its tip_deadline that has
// not yet Tipped (and is not already cancelled) as cancelled. The next
// browse read sees event_state(e) = 'Cancelled' without further fan-out.
func (r *Runner) cancelExpiredBetaEvents(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.events
		SET cancelled_at = now()
		WHERE tip_threshold IS NOT NULL
		  AND tip_deadline < now()
		  AND tipped_at    IS NULL
		  AND cancelled_at IS NULL
	`)
	return err
}

// resolveDoneEventOutcomes writes one attendance_outcomes row per Committed
// Attendee on every Event currently in the 'Done' state. 'show' if a
// checkins row exists for the pair; 'ghost' otherwise. Cancelled events
// are skipped automatically because event_state() returns 'Cancelled' for
// them. The PK + ON CONFLICT make per-row reruns harmless; the
// NOT EXISTS short-circuits per-event scans once the first batch lands.
//
// RETURNING surfaces the user_ids of newly-inserted rows so the per-tick
// reputation fan-out only recomputes the users we actually changed.
// Repeat ticks see no rows returned and skip the recompute loop entirely.
func (r *Runner) resolveDoneEventOutcomes(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		INSERT INTO public.attendance_outcomes (event_id, user_id, outcome)
		SELECT c.event_id, c.user_id,
		       CASE WHEN ck.user_id IS NOT NULL THEN 'show' ELSE 'ghost' END
		  FROM public.commits c
		  JOIN public.events  e  ON e.id = c.event_id
		  LEFT JOIN public.checkins ck
		         ON ck.event_id = c.event_id AND ck.user_id = c.user_id
		 WHERE public.event_state(e) = 'Done'
		   AND NOT EXISTS (
		     SELECT 1 FROM public.attendance_outcomes ao
		      WHERE ao.event_id = c.event_id
		   )
		ON CONFLICT (event_id, user_id) DO NOTHING
		RETURNING user_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Dedup user_ids — a single Done event with one user produces one row,
	// but a multi-user event still maps each user to a single recompute.
	// (Distinct here is a simple precaution; the table's PK already
	// dedups per (event, user).)
	seen := make(map[string]struct{})
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return err
		}
		seen[uid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for uid := range seen {
		if err := reputation.Recompute(ctx, r.pool, uid); err != nil {
			// Non-fatal: log and continue so one bad user doesn't block
			// the rest. The next tick retries because last_recomputed_at
			// stays at its previous value (or absent).
			log.Printf("lifecycle: recompute %s: %v", uid, err)
		}
	}
	return nil
}

// refreshStaleReputations recomputes any reputation row whose
// last_recomputed_at is older than reputationStaleThreshold. This is the
// "daily batch job" from PRD §Reputation system §Decay refresh — running
// it every tick (with the staleness gate) is functionally equivalent to a
// 24hr cron and avoids a separate ticker. Capped per tick to keep a
// backlog from blocking the loop.
func (r *Runner) refreshStaleReputations(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id
		  FROM public.reputation
		 WHERE last_recomputed_at < now() - $1::interval
		 ORDER BY last_recomputed_at ASC
		 LIMIT $2
	`, reputationStaleThreshold.String(), reputationStaleBatchSize)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := reputation.Recompute(ctx, r.pool, id); err != nil {
			log.Printf("lifecycle: refresh %s: %v", id, err)
		}
	}
	return nil
}
