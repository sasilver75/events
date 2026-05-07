// Package lifecycle runs the periodic background work that advances Events
// through their lifecycle independently of any HTTP request: β-Event
// auto-cancel (#32) and Done-state outcome resolution (#34).
//
// Single-replica assumption: this loop runs inside the HTTP server process.
// If we ever scale beyond one replica, every replica will run its own loop
// — the per-row work is idempotent (sticky tipped_at, conditional UPDATE
// guarded by IS NULL), but duplicate queries waste cycles. Add
// pg_try_advisory_lock at the top of each tick before scaling out.
package lifecycle

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTickInterval = 30 * time.Second

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
func (r *Runner) resolveDoneEventOutcomes(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
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
	`)
	return err
}
