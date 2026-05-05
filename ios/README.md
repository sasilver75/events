# ios/

Xcode project root for the Spur iOS app (Swift / SwiftUI).

## Layout

```
ios/
└── Spur/
    ├── Spur.xcodeproj/
    ├── Spur/                  ← app sources (synchronized folder)
    │   ├── SpurApp.swift
    │   ├── ContentView.swift
    │   ├── MapView.swift
    │   ├── MapStyleLight.json
    │   └── Assets.xcassets/
    ├── SpurTests/             ← Swift Testing
    └── SpurUITests/           ← XCUITest
```

The Xcode project uses **synchronized folder** groups (the default in Xcode 26).
Files added under `Spur/Spur/` are auto-included in the build target — no manual
"Add Files to project" step required.

Per [ADR 0018](../docs/adr/0018-ios-bootstrap.md): single Xcode project,
zero local Swift Package Manager (SPM) packages, plain Model-View architecture
with `@Observable` model classes (no separate ViewModel layer).

## Requirements

- **Xcode 26.0** or later
- **iOS 26.0** simulator runtime (download via `xcodebuild -downloadPlatform iOS`
  or Xcode → Settings → Components)
- Minimum deployment target: **iOS 26.0** (see ADR 0018 §4 for the cohort-cost
  reasoning — `FoundationModels` and `DeclaredAgeRange` are load-bearing)

## Run

1. Open `Spur/Spur.xcodeproj` in Xcode
2. Select an iOS 26 simulator from the run-destination menu (e.g. iPhone 17 Pro)
3. ⌘R

The first build resolves SPM dependencies — expect a one-time delay while
MapLibre Native downloads.

## Dependencies

| Package | Version | Notes |
|---|---|---|
| [MapLibre Native](https://github.com/maplibre/maplibre-gl-native-distribution) | `6.26.0` (exact) | Vector-tile renderer; UIKit-based, bridged to SwiftUI via `MapView.swift` |

Pinned to an exact version per ADR 0018 §3 — not `main`, not a range.

## Map / tile pipeline

The map renders Protomaps vector tiles served by the `spur-tiles` Cloudflare
Worker, defined in [ADR 0019](../docs/adr/0019-self-hosted-pmtiles-cloudflare-worker.md).

- **Tile URL** (in `MapStyleLight.json`):
  `https://spur-tiles.sasilver0051.workers.dev/tiles/{z}/{x}/{y}.mvt`
- **Worker source:** [`workers/spur-tiles/`](../workers/spur-tiles/)
- **Glyphs / sprites:** `protomaps.github.io/basemaps-assets` (public, no key)

`MapStyleLight.json` is a hand-rolled minimal Protomaps-schema style: background,
earth, water, parks, road kinds (highway → minor), country boundaries, locality
and neighbourhood labels. Replace with a richer theme (e.g. the Protomaps
`themes-base` output) when product UI demands it.

## Signing

**Currently `Team = None` (simulator-only).** A paid Apple Developer Program
account is pending; physical-device runs, TestFlight, and entitlements that
require a paid team (push notifications, Sign In with Apple, App Groups,
`DeclaredAgeRange` on device) are deferred until it lands.

When the account is active: project → target Spur → Signing & Capabilities,
switch Team from `None` to the new team for both `Spur` and the test targets.

## Architecture

Plain Model-View with `@Observable` model classes and injected services
(per ADR 0018 §5). No ViewModel layer. Services (network, location, auth)
will be plain Swift types — `actor` for I/O-bound work, `final class`
otherwise — injected via `Environment` or constructor.

Service protocols are not introduced for the sake of mocking. Per
[`CLAUDE.md`](../CLAUDE.md), integration tests hit a real Postgres rather
than mocks; UI flows get sparing XCUITest coverage on critical paths
(Commit, check-in).

## Cross-references

- [`PRD-v0.md`](../PRD-v0.md) — product spec
- [`CONTEXT.md`](../CONTEXT.md) — domain vocabulary (use these terms in code)
- [`docs/adr/`](../docs/adr/) — architectural decisions
- [`CLAUDE.md`](../CLAUDE.md) — project-wide conventions
