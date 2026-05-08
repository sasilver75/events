// Package reputation implements RecomputeReputation per ADR 0008 and PRD
// §Reputation system.
//
// The formula is two-input:
//
//   - Behavioral: severity-weighted Bayesian Bernoulli over
//     attendance_outcomes ('show' / 'ghost'; 'flake' lands later — see
//     ADR 0008). Show contributes success-weight 1.0; Ghost contributes
//     failure-weight 3.0; Flake (when it lands) contributes 1.5. Time
//     decay is 2-year half-life. Prior is Beta(α=4, β=1).
//
//   - Flag factor: multiplicative penalty over flags.cast_at, also
//     time-decayed. flag_factor = max(0.4, 1 − 0.12 × Σ decay).
//
// score = round(behavioral × flag_factor), clamped to [0, 100].
//
// Implementation note: the formula lives in a single CTE. Doing it in SQL
// keeps RecomputeReputation a fixed-cost call (one round-trip, no pgx
// scan-loop) and avoids ferrying outcome rows into Go just to sum them.
// The CTE is public-schema-only — no PL/pgSQL, no triggers — so this is
// "business logic in Go that happens to be expressed as a single SQL
// statement", per CLAUDE.md / ADR 0005. The per-event aggregation is a
// view-shape concern, not a rule.
//
// Idempotent: every call recomputes from scratch and upserts the
// reputation row. Safe to call repeatedly (Done fan-out, hard-flag
// submit, daily refresh).
package reputation

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// halfLifeDays is the 2-year half-life from ADR 0008. extracted so a
// future tuning slice can change it without rewriting the CTE.
const halfLifeDays = 730.0

// Querier is the subset of pgx we need. *pgxpool.Pool and pgx.Tx both
// implement it, so RecomputeReputation can run in or out of a transaction
// (the feedback handler runs it inside a tx; the lifecycle Runner runs it
// outside).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Recompute reads attendance_outcomes + flags for the user and upserts
// the reputation row. Reads in the same transaction (when q is a tx) see
// the writes that motivated the recompute — that's why the feedback
// handler MUST run Recompute inside its tx after writing flags, and why
// the Done fan-out runs OUTSIDE its insert (the outcome rows are already
// committed by the time fan-out fires).
//
// attendee_event_count counts attendance_outcomes rows (Show + Ghost).
// host_score / host_event_count stay at 0/NULL until the host-rep slice
// lands (ADR 0008 §host slice deferred).
func Recompute(ctx context.Context, q Querier, userID string) error {
	const sql = `
WITH
  cfg AS (
    SELECT
      $2::float8        AS half_life_days,
      4.0::float8       AS prior_alpha,
      1.0::float8       AS prior_beta,
      0.12::float8      AS flag_per_unit_penalty,
      0.4::float8       AS flag_factor_floor
  ),
  outcomes AS (
    SELECT
      ao.outcome,
      EXTRACT(EPOCH FROM (now() - ao.recorded_at)) / 86400.0 AS age_days
    FROM public.attendance_outcomes ao
    WHERE ao.user_id = $1::uuid
  ),
  outcome_agg AS (
    SELECT
      COALESCE(SUM(
        CASE outcome
          WHEN 'show'  THEN 1.0 * power(0.5, age_days / cfg.half_life_days)
          ELSE 0.0
        END
      ), 0.0) AS success_weight,
      COALESCE(SUM(
        CASE outcome
          WHEN 'ghost' THEN 3.0 * power(0.5, age_days / cfg.half_life_days)
          ELSE 0.0
        END
      ), 0.0) AS failure_weight,
      COUNT(*)                               AS event_count
    FROM outcomes, cfg
  ),
  flag_agg AS (
    SELECT
      COALESCE(SUM(
        power(0.5, (EXTRACT(EPOCH FROM (now() - f.cast_at)) / 86400.0) / cfg.half_life_days)
      ), 0.0) AS weighted_flags
    FROM public.flags f, cfg
    WHERE f.target_user_id = $1::uuid
  ),
  scored AS (
    SELECT
      cfg.prior_alpha + outcome_agg.success_weight  AS posterior_alpha,
      cfg.prior_beta  + outcome_agg.failure_weight  AS posterior_beta,
      GREATEST(
        cfg.flag_factor_floor,
        1.0 - cfg.flag_per_unit_penalty * flag_agg.weighted_flags
      ) AS flag_factor,
      outcome_agg.event_count                       AS event_count
    FROM cfg, outcome_agg, flag_agg
  ),
  final AS (
    SELECT
      LEAST(100, GREATEST(0,
        round(
          (posterior_alpha / NULLIF(posterior_alpha + posterior_beta, 0)) * 100.0
          * flag_factor
        )::int
      )) AS attendee_score,
      event_count
    FROM scored
  )
INSERT INTO public.reputation (
    user_id, attendee_score, host_score,
    attendee_event_count, host_event_count, last_recomputed_at
)
SELECT $1::uuid, attendee_score, NULL, event_count, 0, now()
  FROM final
ON CONFLICT (user_id) DO UPDATE
   SET attendee_score       = EXCLUDED.attendee_score,
       attendee_event_count = EXCLUDED.attendee_event_count,
       last_recomputed_at   = EXCLUDED.last_recomputed_at
`
	_, err := q.Exec(ctx, sql, userID, halfLifeDays)
	return err
}
