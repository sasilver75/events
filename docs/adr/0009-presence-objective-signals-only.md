# Presence is resolved by objective signals only — bounded passive geofence + check-in tap

Show vs Ghost is determined exclusively by **objective signals**: a successful check-in tap (geofence-gated, ~50m of the pin) *or* passive location confirmation during the event window. Peer attestation does **not** enter the Show/Ghost decision. Flags are an orthogonal subjective channel that drives `flag_factor`, never presence determination.

This ADR supersedes [ADR 0002](./0002-geofence-checkin-no-peer-attestation.md), which had the right principle but a disposition matrix that violated it.

## Why supersede ADR 0002

ADR 0002 opens with the correct principle — "subjective signals stay in the subjective lane; objective presence is resolved by GPS-gated check-in." Its disposition matrix, however, used a peer-applied "Ghost" flag to disambiguate "didn't check in but no flag" (Soft Ghost, light ding) from "didn't check in and was flagged" (Ghost, full ding). That made a peer flag a *presence* signal in disguise, contradicting the opening principle. This ADR removes that contradiction by making the Show/Ghost determination purely objective.

The user-facing consequence of the old matrix — Soft Ghost as a separate "ambiguous" disposition — is also gone. Either presence is confirmed (Show) or it isn't (Ghost). The ambiguity that Soft Ghost was trying to express is better handled by the passive-presence path: users who would previously have been Soft-Ghosted because they "forgot to tap" now get auto-confirmed via passive location, provided they granted permission.

## What replaces it

**Two paths to Show, both objective:**

1. **Check-in tap.** As before — geofence-gated at ~50m around the event pin. Anti-fraud (you can't claim presence remotely).
2. **Passive location confirmation.** For users with iOS "Always Allow" location, the app passively checks whether any GPS sample places them within the wider passive-presence radius (~150m) during the event window (15 minutes before scheduled start through scheduled end). A single sample inside the polygon during the window suffices. Leaving during the event still counts as Show in v0.

If neither produces presence confirmation and the Attendee didn't Withdraw, the outcome is **Ghost**. No third "Soft Ghost" state.

**Flags are orthogonal.** A peer 👎 with a hard reason ("would not attend with again," "concerning behavior") drives the multiplicative `flag_factor` in the reputation formula, but never the Show/Ghost decision. A flag is **invalidated** if objective location data demonstrably places the flagged user elsewhere during the event window — they couldn't have been there to misbehave. If location data is unavailable for the flagged user, the flag is accepted.

**Two radii, deliberate asymmetry:**

- **50m for check-in tap** — strict, anti-fraud.
- **150m for passive confirmation** — wider, forgiving on GPS noise. Background GPS readings are harder to spoof than user-controlled taps, so we tune toward not missing real attendees.

**Permission posture.** First Commit triggers the iOS "Always Allow" location prompt with framing copy that names both the value (auto-confirm without tapping) and the boundary (stops when event ends). Decline → check-in-tap-only fallback.

## Considered alternatives

- **Continuous-geofence-of-everyone-everywhere.** What ADR 0002's "v1 enhancement" line implied. Rejected for v0 specifically because of battery/privacy concerns. Our bounded version (only Committed events, only event window, only inside polygon, only with explicit permission) avoids those costs.
- **Per-event opt-in toggle separate from OS permission.** Rejected: the OS permission gates whether passive tracking is even *possible*, so a per-event toggle on top of that is double-consent theater.
- **Keep peer attestation as a Show/Ghost tie-breaker.** Rejected: that's the contradiction this ADR exists to remove.

## Consequences

- **The reputation formula loses Soft Ghost.** Severity weights become Show/Withdraw/Flake/Ghost/Early Withdraw. ADR 0008 and the explainer both need to drop the Soft Ghost row.
- **Battery/privacy posture is honest.** Tracking is bounded in time (event window only), space (within polygon), scope (Committed events only), and consent (explicit "Always Allow"). Stops as soon as the event ends or the user leaves the polygon.
- **The 150m and 50m radii are tunable knobs.** Both will need calibration once we have real data. v0 starting points only.
- **Indoor / GPS-noisy venues will produce edge cases.** Users in a basement bar inside the 150m polygon might not get GPS samples. They fall back to the tap, or get Ghosted if neither produces a Show. Acceptable for v0.
- **Flag-validity check is a real implementation requirement.** When the Go server receives a flag, it must check the flagged user's location during the event window. If the location data shows them demonstrably elsewhere, the flag is rejected. If location data is unavailable, the flag is accepted. This requires keeping passive-location samples for the event window, scoped per-event-attendee.
