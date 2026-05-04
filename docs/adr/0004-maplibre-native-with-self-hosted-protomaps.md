# MapLibre Native + self-hosted Protomaps tiles for the map layer

v0 uses **MapLibre Native** (Metal renderer, Swift bindings) as the iOS map SDK, with a forked **Protomaps Basemaps** style for cartography and a self-hosted **`.pmtiles`** file on Cloudflare R2 as the tile source. Mapbox Maps SDK, Apple MapKit, and Google Maps SDK are rejected for v0.

## Why

The product wedge depends on a brand-distinct, playful map look — Apple MapKit and Google Maps SDK can't deliver custom cartography of that kind. That narrows the choice to Mapbox vs MapLibre.

Between those two, the deciding factor is that we have no designer. Mapbox Studio's main advantage is designer ergonomics for building custom styles; for an engineer dialing a forked style to "quiet, readable, on-brand," that advantage largely evaporates. Forking Protomaps Basemaps gets us ~60% of the way to a distinctive look on day one, and the playful Pokémon-Go-style personality is carried by the *animated pin annotations* (rendered in SwiftUI/Metal/Lottie) layered on top of the basemap — not by the basemap itself.

MapLibre additionally gives us:
- No per-MAU billing risk, ever. Mapbox's pricing kicks in at 25k MAU and scales meaningfully past that.
- No third-party telemetry on user location/usage by default.
- Full openness — we can fork or patch the SDK if needed.
- Tile-source flexibility — we host our own `.pmtiles`, no vendor in the data path.

Self-hosting on R2 is essentially free at v0 scale (one static file, zero egress fees) and is durable knowledge to build (OSM, vector tiles, PMTiles format).

## Considered alternatives

- **Mapbox Maps SDK for iOS.** Best-in-class for custom cartography via Mapbox Studio, polished Swift surface, newer features land first. Rejected because (a) we have no designer to benefit from Studio, (b) per-MAU billing is a real future cost we'd rather not architect into v0, and (c) MapLibre is similar enough on iOS that the migration door stays open if we want to flip later.
- **Apple MapKit.** Free, integrated, no MAU bill. Rejected: custom cartography is too constrained — we cannot achieve a brand-distinct look, and the wedge depends on the look.
- **Google Maps SDK.** Capable but Google's visual language permeates and theming is more constrained than Mapbox or MapLibre. Also association cost — the product is positioned against Google's "places" model.

## Consequences

- "MapLibre Native" is the C++ engine with Swift bindings (Metal-backed) — not a JS bridge. The name is historical, distinguishing it from MapLibre GL JS.
- We commit to ~1–2 weeks of part-time style fiddling on a Protomaps Basemaps fork to reach an on-brand look. Acceptable bar is "quiet, readable, on-brand color" — not "great cartography." The pin treatment carries the playful weight.
- Tile pipeline ops surface: regenerate the `.pmtiles` extract from OSM data periodically (monthly cadence is fine). One command, but a real ongoing task.
- We deliberately avoid Mapbox-proprietary style features and Mapbox's directions/geocoding/search APIs to keep migration to (or from) Mapbox a real option.
- If we later need geocoding, search, or routing, those come from separate providers (Stadia, MapTiler, or self-hosted) — they are not bundled with the map SDK.
