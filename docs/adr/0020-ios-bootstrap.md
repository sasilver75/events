# iOS bootstrap: project layout, modules, map integration, deployment target, architecture pattern

**Status:** Accepted (2026-05-04).

The v0 iOS app is a single Xcode project at `/ios/` with no local Swift Package Manager (SPM) packages, MapLibre Native added via SPM and pointed at the Cloudflare Worker tile URL from [ADR 0021](./0021-self-hosted-pmtiles-cloudflare-worker.md), a minimum deployment target of **iOS 26.0**, and a plain Model-View (MV) architecture using `@Observable` models with injected services. Multi-project workspaces, xcodegen/Tuist, premature SPM splits, MVVM ceremony, and The Composable Architecture (TCA) are all rejected for v0.

## Why

Five decisions, captured in one ADR because they form the foundational shape of the iOS app and are interdependent enough that splitting them obscures the picture.

### 1. Single Xcode project at `/ios/`, `project.pbxproj` committed

Standard modern Swift app layout. One `.xcodeproj`, optional SPM modules inside the workspace if any get extracted later. This is the lowest-friction setup and matches what Xcode's project templates produce.

`project.pbxproj` is checked into git as-is. xcodegen and Tuist (which regenerate the project from a YAML or Swift spec) are deferred. Their main payoff is avoiding merge conflicts on `project.pbxproj`, which is a real problem on multi-contributor teams. For a solo developer it's not worth the build-tool dependency. Revisit if a second contributor joins.

A multi-project workspace (separate `.xcodeproj` files glued by an `.xcworkspace`) is overkill for one app target. A pure-SPM executable (no Xcode project, only `Package.swift`) is technically possible but iOS app entitlements, `Info.plist`, asset catalogs, and signing are still smoother through a project file.

### 2. Zero local SPM packages at bootstrap

Everything lives in the main app target. No `Core`, no `APIClient`, no `DesignSystem` package. The rule: **add a local SPM package only when one of the following is true** —
- A chunk of code needs to compile and test in isolation from the iOS app shell.
- A chunk gets reused by a second target (widget extension, share extension, future Apple Watch companion).
- Compile times degrade enough that smaller modules materially help incremental builds.

None of these are true at v0. The "obvious" candidates (`APIClient`, `DesignSystem`, `MapView`) each fall apart under scrutiny: `APIClient` is roughly 50 lines wrapping `URLSession`, `DesignSystem` is premature without a designer or a second consumer, `MapView` is one `UIViewRepresentable`. Inlining them costs nothing now and avoids module-boundary friction during early iteration.

A watch companion is the most likely future trigger because watch extensions cannot link to the main app target — they need a shared package. That's a known but distant possibility per ADR 0003 (iOS-first, Android deferred); we extract the shared layer when that work begins, not before.

### 3. MapLibre Native via SPM, tile source per ADR 0021

MapLibre Native is added through Swift Package Manager, pinned to a specific version tag (not `main`). The official package ships a precompiled `MapLibre.xcframework`. CocoaPods, Carthage, and manual binary vendoring are rejected — SPM is the modern default and integrates with Xcode without extra tooling.

The tile source is the Cloudflare Worker URL defined in ADR 0021 (self-hosted PMTiles in front of R2). The iOS client sees a vanilla XYZ tile source and holds no upstream API key. The full reasoning for that pipeline — request-shape adaptation, edge caching, cost — lives in ADR 0021.

The SwiftUI bridge is a thin `UIViewRepresentable` wrapper around `MLNMapView`. MapLibre Native is UIKit-based (the name is historical, distinguishing it from MapLibre GL JS — the Metal-backed Swift bindings are what runs on iOS). The wrapper is roughly 30 lines and lives in the main app target, not a package.

### 4. Minimum deployment target: iOS 26.0

iOS 26.0 (released September 2025) is the floor. iOS 17 and iOS 18 were considered and rejected.

Two iOS 26 features are direct hits on Spur product requirements rather than aesthetic refinements:

- **`FoundationModels` framework** — on-device large-language-model access. Free, private, offline, no server round-trip. This is load-bearing for content moderation: a stranger-meeting product needs moderation on user-written event content (titles, descriptions, chat messages), and doing it on-device avoids both the per-call cost of an Anthropic or OpenAI API and the privacy issue of shipping user-generated content to a third party. Without `FoundationModels`, the alternative is standing up a server-side moderation pipeline against a paid LLM provider.
- **`DeclaredAgeRange` framework** — privacy-preserving OS-level age attestation. Spur is 18+ only (PRD-v0); without this framework we'd build a custom age-gate flow (date-of-birth screen, custom verification, stored DOB). With it, the OS hands us a "user is 18+" signal without us collecting or storing the underlying date.

The Liquid Glass design system also lands in iOS 26 and is the current visual baseline; consistent with ADR 0003's emphasis on a *premium, polished feel* as a core part of the wedge.

Adoption cost: per StatCounter (US, April 2026), iOS 26.x is roughly 71% of all iPhones. Our cohort (LA, 18+, early adopters per ADR 0003) skews higher — plausibly 85–90%, though that's a directional estimate. Accepting the ~10–15% cohort cost is consistent with the "be the premium app" posture from ADR 0003, where the same logic justified iOS-only over Android: the premium positioning earns the right to require recent hardware.

iOS 18 (Sept 2024) was the runner-up. It includes a limited subset of what becomes `FoundationModels` in iOS 26, plus `ControlWidget` and tinted icons. The cohort cost is lower (~3–5% excluded), but it does not give us the moderation or age-gate APIs, which are the load-bearing reasons for the iOS 26 floor.

iOS 17 (Sept 2023) was the original recommendation in the bootstrap issue. It includes `@Observable` (the macro that underpins decision 5 below) and was correct *before* we knew iOS 26 contained product-relevant APIs. Now superseded by the iOS 26 analysis.

The **patch version** is pinned to `.0` rather than a later minor (26.3, 26.4) because iOS minor releases are overwhelmingly OS bug fixes, not new app-developer APIs. Excluding users on 26.0 buys nothing. If a specific later-minor API ever becomes load-bearing for one feature, we wrap that one call in `if #available(iOS 26.x, *)` rather than bumping the global floor.

### 5. Plain MV with `@Observable` models and injected services

Apple's modern recommended pattern. Views observe `@Observable` model classes directly; no separate ViewModel layer. Services (network, location, auth) are plain Swift classes or actors injected via SwiftUI's `Environment` or constructor injection on the model.

`@Observable` (the macro, not a protocol) is what makes this pattern clean. Pre-`Observable`, you needed a ViewModel to bundle `@Published` properties because models alone weren't observable. Post-`Observable`, the model itself is the observable thing, SwiftUI's diffing only re-renders what changed, and the ViewModel layer becomes file-per-screen ceremony with no payoff.

Architecture choices considered and rejected:

- **MVVM (Model-View-ViewModel).** Common in Swift codebases historically; redundant with `@Observable`. The ViewModel layer becomes a thin pass-through that adds files without adding capability. Rejected.
- **TCA (The Composable Architecture, Point-Free).** Redux-style with explicit `State`, `Action`, `Reducer`, `Effect`. Strong on testability and time-travel debugging; heavy on boilerplate (~3x the code per screen) and opinionated about dependency injection. The payoff scales with team size and need for strict reducer-level testing. For a solo-dev v0 it is overkill. Migration door stays open: any specific screen can adopt TCA later without forcing the rest of the app to follow.
- **VIPER, Clean Swift.** UIKit-era patterns that no longer fit SwiftUI's data-flow model. Not seriously considered.

**Service shape.** Cross-cutting concerns — network client, location, auth, analytics — are plain Swift types (`actor` for anything I/O-bound, `final class` otherwise). Injected at construction or through `Environment`. No global singletons unless something is genuinely global (one network client for the app), and even then scope it.

**Testing posture.** No protocols for the sake of mocking. Service protocols introduced only when there's a concrete test that benefits from a fake. Per CLAUDE.md, integration tests hit real Postgres, not mocks. Pure-logic functions get unit tests; UI flows get sparing XCUITest coverage on the few critical paths (Commit, check-in).

## Considered alternatives

Captured inline in each section above to keep the rejected options next to the corresponding decisions. The ones worth surfacing at a glance:

- xcodegen / Tuist — deferred, revisit on second contributor.
- Local SPM split for `APIClient` / `DesignSystem` — deferred, revisit on second consumer or compile-time pain.
- Mapbox / Apple MapKit / Google Maps SDK — already rejected by ADR 0004.
- Direct iOS PMTiles plugin — already rejected by ADR 0021.
- iOS 17 / iOS 18 floors — rejected because iOS 26 contains product-relevant APIs (`FoundationModels`, `DeclaredAgeRange`).
- MVVM, TCA — rejected for v0; TCA migration door remains open per-feature.

## Cross-references

- [ADR 0003](./0003-ios-first-native.md) — iOS-first native, premium-positioning posture that justifies the iOS 26 cohort cost.
- [ADR 0004](./0004-maplibre-native-with-self-hosted-protomaps.md) — MapLibre Native + Protomaps + R2 primitives.
- [ADR 0021](./0021-self-hosted-pmtiles-cloudflare-worker.md) — tile pipeline (Cloudflare Worker in front of R2) referenced by decision 3.

## Consequences

- The Xcode project at `/ios/` is the single source of truth for app structure. Adding a local SPM package is a deliberate event with a documented trigger, not a default.
- `project.pbxproj` merge conflicts are a known future risk if a second contributor joins. The mitigation is to revisit xcodegen/Tuist at that point, not preemptively.
- iOS 26 floor excludes ~10–15% of the cohort today. Re-evaluate the floor in roughly 12 months, when iOS 27 has shipped and iOS 26 adoption is approaching saturation.
- `FoundationModels` becomes the moderation primitive. Any moderation logic should be designed to run on-device by default; falling back to a server-side moderator becomes a deliberate exception, not the baseline.
- `DeclaredAgeRange` becomes the 18+ gate. The auth/onboarding flow needs to incorporate it rather than collecting date-of-birth.
- Liquid Glass is the visual baseline. Custom UI work should compose with it rather than re-skinning around it.
- Plain MV means `@Observable` model classes proliferate as the app grows. That's fine; the alternative (a ViewModel per screen) is strictly more files for the same outcome.
- No service protocols by default means swapping implementations for tests is a deliberate refactor, not a pre-built seam. This is the intended trade-off — simpler code now, refactor when a real testing need appears.
