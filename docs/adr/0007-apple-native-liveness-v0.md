# Apple-native liveness for v0 selfie verification

v0 implements selfie liveness using **Apple-native primitives** — `AVFoundation` for camera capture and the `Vision` framework for face detection — with a blink or head-turn challenge during signup. Third-party passive-liveness SDKs (Persona, AWS Rekognition Liveness, FaceTec) are explicitly **deferred** and gated on the project moving from personal use toward distribution.

## Why

v0 is a personal/learning project, not a commercial launch. Per-check verification fees ($0.10–$1.00 each on third-party SDKs) are unjustified spend at this stage; the wedge has not been validated, signup volume is tiny, and the safety promise made in product copy is aspirational rather than operative.

A homegrown blink/head-turn check using Apple's own face-detection primitives is sufficient to:
- Deter casual bot signups.
- Make the selfie meaningfully "live" rather than a stored photo upload.
- Let the signup flow feel like a real product without a blocking integration build.

It is consciously **insufficient** to:
- Defeat a motivated attacker holding up a printed photo, a video on another phone, or a deepfake.
- Support the "verified people" promise the PRD makes to women considering attending stranger Events.

The PRD has been updated to call this out honestly — v0's safety floor is "phone + light selfie liveness," and the upgrade to a third-party passive SDK is named as a prerequisite for moving toward distribution.

## Considered alternatives

- **Skip liveness entirely (selfie-as-photo only).** Rejected: even at v0 scope, "live in-app capture" is part of what makes the product feel intentional and not a bot-magnet. The marginal cost of a blink challenge is days, not weeks.
- **Third-party passive-liveness SDK (Persona, AWS Rekognition Liveness, FaceTec).** Rejected for v0 only: per-check fees and integration overhead are unjustified at the personal-project stage. This is the explicit upgrade path before distribution.
- **Defer liveness to a later trust check (gate hosting/seeding behind it).** Rejected: forces a future awkward UX retrofit where existing users get prompted to re-verify.

## Consequences

- v0's safety story is honest-but-weak. Marketing/product copy must not over-promise verification strength while v0's mechanism is in place. The PRD's safety section now reflects this explicitly.
- The Vision-framework liveness code is replaceable. Migrating to a third-party SDK later means swapping the liveness module — meaningful work but not a rewrite, and the UX surface (camera capture screen) stays roughly the same.
- The selfie itself (the captured frame after the liveness challenge passes) is stored as the user's default avatar regardless of the verification mechanism — that part does not change with the upgrade.
- If we ever begin distribution, the upgrade to a third-party passive-liveness SDK is **blocking work**, not optional. This decision must be revisited at that gate.
