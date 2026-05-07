// Package lifecycle runs the periodic background work that advances Events
// through their lifecycle independently of any HTTP request — currently only
// β-Event auto-cancel (#32). The Done-poller for outcome resolution (#34)
// will hook into the same Run loop when it lands.
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
func (r *Runner) RunOnce(ctx context.Context) error {
	return r.cancelExpiredBetaEvents(ctx)
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
