# Reputation has two scoring inputs — Behavioral and Flags — with deliberate asymmetry

> **Revised** after ADR 0009 collapsed Soft-Ghost into Ghost, and after the rating model shifted from 1–5 stars to 👍 / 👎 / skip. Earlier draft of this ADR described three weight classes (Behavioral / Flags / Ratings); the current model is two — see below.

The reputation score (0–100, Bayesian-smoothed, time-decayed) takes two structurally distinct inputs:

1. **Behavioral** — Show / Withdraw / Flake / Ghost outcomes from objective signals (check-in tap or passive location, per ADR 0009). Severity-weighted, drives the score multiplicatively via the Bayesian posterior.
2. **Flags** — peer 👎s with at least one "hard reason" selected ("would not attend with again," "concerning behavior," "made me uncomfortable"). Multiplicative penalty (`flag_factor`).

Captured-but-not-scored signals: 👍 thumbs-ups and 👎s with the generic "I just didn't like them" reason. These are surfaced to the recipient as bundled feedback ("3 people gave you 👍 from your last events; 1 person didn't connect with you") but do not move the score. Score = trustworthiness, not popularity.

The asymmetry between behavioral and flags is **deliberate**, and so is the absence of soft-negative scoring.

## Why the asymmetry

A Rating is a quick numeric — "I had an okay time" might land as 4 stars instead of 5 with very little intentional weight behind it. The signal is noisy and easily skewed by mood, weather, or whether the person had a good parking spot. Treating ratings as a strong score driver would make reputation a function of mood-adjusted typing.

A Flag is a different act entirely. An Attendee actively chose to take a labeled action — "I would not attend with this person again" or "concerning behavior" — that names a specific other person and a specific concern. The signal is intentional, named, and asymmetric (no one accidentally flags someone). Treating it as equivalent to a star drop would erase the *meaning* the flagger put into the act.

The product values driving the asymmetry: **a user who shows up reliably but is socially unsafe should not score well.** Reliability without trustworthiness is not what the system rewards. The previous formulation (subjective ratings + flags weighted lower as a unified bucket) made it possible for a "reliable creep" to outrank a "nice flaker," which contradicts the social health the platform is trying to cultivate.

## Concrete formula (illustrative — tunable)

```
HALF_LIFE_DAYS = 730
decay(age) = 0.5 ** (age / HALF_LIFE_DAYS)

# Behavioral: severity-weighted Bayesian Bernoulli with time-decayed evidence
event_weights = {
  Show:           {success: 1.0, failure: 0.0},
  Flake:          {success: 0.0, failure: 1.5},
  Ghost:          {success: 0.0, failure: 3.0},
  Early_Withdraw: {success: 0.0, failure: 0.0},
}
posterior_α = 4.0 + Σ (e.success_weight * decay(e.age_days))
posterior_β = 1.0 + Σ (e.failure_weight * decay(e.age_days))
behavioral  = (posterior_α / (posterior_α + posterior_β)) * 100

# Flag factor: multiplicative penalty, time-decayed counts
weighted_flags = Σ decay(flag.age_days)
flag_factor    = max(0.4, 1 - 0.12 * weighted_flags)

# Final
score = clamp(round(behavioral * flag_factor), 0, 100)
```

The floor on `flag_factor` (0.4) bounds coordinated-harassment risk — even unbounded flags can drop a user only to 40% of their behavioral score. The deferred v1 flagger-credibility weighting (already in CONTEXT.md) is the longer-term defense against bad-faith flagging.

Soft-Ghost is **not** in the table. Per ADR 0009, the Soft-Ghost disposition was removed when peer attestation stopped being a presence signal — there is no longer an "ambiguous" middle state for behavioral.

## Considered alternatives

- **Unified subjective bucket (ratings + flags weighted equally and lightly).** Earlier formulation, rejected. Loses the semantic asymmetry between intentional-named-flag and casual-positive/negative reaction. Allowed reliable-creep to outrank nice-flaker.
- **Additive flag penalty instead of multiplicative.** Tried earlier (`-7 per flag, capped at -25`). Rejected: with 0–100 behavioral range and ±25 max flag penalty, reliable creep (95) still outscored nice flaker (60) numerically. Multiplicative penalty (`× 0.52` for 4 flags) actually delivers the values.
- **Flags as soft-block instead of score component.** A flagged user gets surfaced for moderation review rather than scored down. Deferred — moderation infrastructure is v0-minimal and not load-bearing on the anti-creep posture.
- **Including soft-negatives ("I just didn't like them") in the score.** Rejected: score is trustworthiness, not popularity. Soft 👎s are captured for the recipient's bundled feedback display but don't move the dial.

## Consequences

- The flag UI carries real weight; users should understand that flagging is a meaningful act, not a casual reaction. Copy at the flag affordance must reflect this.
- The 0.4 floor on `flag_factor` is load-bearing — without it, coordinated harassment can tank a target's score arbitrarily. The floor value is tuning territory.
- A future contributor reading the formula will see only two scoring inputs (behavioral and flag count) and may wonder where the 👍/👎 ternary went. This ADR explains: positive and soft-negative reactions are bundled-feedback signals to the recipient, not score inputs.
- The deferred flagger-credibility weighting (in CONTEXT.md) becomes more important under this multiplicative structure — flags carry serious weight, so bad-faith flag patterns need eventual defense beyond the floor.
- Time-decay (2-year half-life) means old behavior fades. Old Ghosts and old flags both lose weight at the same rate. Tunable.
