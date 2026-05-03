# iOS-first, native (Swift/SwiftUI) for v0

v0 ships as a single native iOS app written in Swift/SwiftUI. Android is explicitly deferred — not killed, but not on the v0 critical path. Cross-platform frameworks (React Native, Flutter) and PWA are rejected for v0.

## Why

The wedge depends on the *feel* of the product more than most apps: a playful tilted-2D map, animated Pokémon-Go-style pins, smooth selfie liveness capture, snappy push-to-Commit interactions. One platform done excellently beats two platforms done acceptably, and at v0 we cannot afford to spread polish across two codebases.

LA's 18+ early-adopter cohort skews heavily iOS, so an Android delay is unlikely to kneecap the network effect during the validation window. Once show-rate and Tip-rate validate the wedge, an Android port becomes a known-cost follow-up rather than a parallel bet.

## Considered alternatives

- **React Native or Flutter (single cross-platform codebase).** Rejected for v0: the map + custom pin animation surface is where cross-platform frameworks struggle most, and the camera/liveness path benefits from deep native integration. Worth revisiting if Android becomes urgent before the wedge is validated.
- **Native iOS + native Android in parallel from day one.** Rejected: doubles the team or halves the velocity. Not justified before the wedge is proven.
- **PWA / web-first.** Rejected: location, push notifications, camera-based liveness, and animated map performance all degrade meaningfully on the web; the product posture is fundamentally native.

## Consequences

- Marketing, App Store presence, and demo content target iOS only for v0. "Coming to Android" messaging needs a deliberate stance — likely a waitlist rather than a vague promise.
- Backend APIs should remain client-agnostic so an Android client can be added without server rework.
- Class-signaling risk: iPhone-only carries connotations that can contradict the inclusive social vibe. Mitigation is messaging, not engineering — but worth tracking in user research.
