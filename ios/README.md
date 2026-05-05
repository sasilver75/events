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
    │   ├── AttributionView.swift
    │   ├── MapStyleLight.json
    │   └── Assets.xcassets/
    ├── SpurTests/             ← Swift Testing
    └── SpurUITests/           ← XCUITest
```

The Xcode project uses **synchronized folder** groups (the default in Xcode 26).
Files added under `Spur/Spur/` are auto-included in the build target — no manual
"Add Files to project" step required.

Per [ADR 0020](../docs/adr/0020-ios-bootstrap.md): single Xcode project,
zero local Swift Package Manager (SPM) packages, plain Model-View architecture
with `@Observable` model classes (no separate ViewModel layer).

## Requirements

- **Xcode 26.0** or later
- **iOS 26.0** simulator runtime (download via `xcodebuild -downloadPlatform iOS`
  or Xcode → Settings → Components)
- Minimum deployment target: **iOS 26.0** (see ADR 0020 §4 for the cohort-cost
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

Pinned to an exact version per ADR 0020 §3 — not `main`, not a range.

## Map / tile pipeline

The map renders Protomaps vector tiles served by the `spur-tiles` Cloudflare
Worker, defined in [ADR 0021](../docs/adr/0021-self-hosted-pmtiles-cloudflare-worker.md).

- **Tile URL** (in `MapStyleLight.json`):
  `https://spur-tiles.sasilver0051.workers.dev/tiles/{z}/{x}/{y}.mvt`
- **Worker source:** [`workers/spur-tiles/`](../workers/spur-tiles/)
- **Glyphs / sprites:** `protomaps.github.io/basemaps-assets` (public, no key)

`MapStyleLight.json` is the unmodified output of [`@protomaps/basemaps`](https://github.com/protomaps/basemaps)
for the `LIGHT` flavor, with the `protomaps` source pointed at the `spur-tiles`
Worker. It is a build artifact — do not hand-edit. Spur-specific overlays
(currently the 3D buildings extrusion at zoom ≥14) live in `MapView.swift` and
are added at runtime via `MLNStyle.addLayer`, keeping the vendored JSON a clean
themes-base export so it stays cleanly regeneratable when the upstream schema
updates.

### Regenerating the map style

```sh
cd style
npm install
npm run generate
```

This rewrites `Spur/Spur/MapStyleLight.json`. Bump
`@protomaps/basemaps` in `style/package.json` to pick up upstream changes.

## Attribution

The MapLibre Native built-in logo and attribution button are hidden in
`MapView.swift` for visual cleanliness. To stay compliant with OpenStreetMap's
Open Database License (ODbL) and the Protomaps usage terms, attribution is
surfaced via `AttributionView.swift` — a SwiftUI screen reachable from the
info button on the map. It credits OpenStreetMap (data), Protomaps (tiles),
and MapLibre Native (renderer); each row opens its source in
`SFSafariViewController`.

The screen gates distribution: any TestFlight or App Store build must ship
with it reachable. Sim/dev runs do not require it but should not be shipped
to real users without it.

## Signing

**Currently `Team = None` (simulator-only).** A paid Apple Developer Program
account is pending; physical-device runs, TestFlight, and entitlements that
require a paid team (push notifications, Sign In with Apple, App Groups,
`DeclaredAgeRange` on device) are deferred until it lands.

When the account is active: project → target Spur → Signing & Capabilities,
switch Team from `None` to the new team for both `Spur` and the test targets.

## Architecture

Plain Model-View with `@Observable` model classes and injected services
(per ADR 0020 §5). No ViewModel layer. Services (network, location, auth)
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
