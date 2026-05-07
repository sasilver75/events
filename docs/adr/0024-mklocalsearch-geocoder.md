# MKLocalSearch as the v0 geocoder, behind a `GeocodingService` protocol

**Status:** Accepted (2026-05-07).

The location picker's place-search field uses Apple's `MKLocalSearchCompleter` for autocomplete, `MKLocalSearch` for resolving a tapped completion to coordinates, and `CLGeocoder.reverseGeocodeLocation` for the post-pin hint label. All three are wrapped behind a `GeocodingService` Swift protocol so the Android-day implementation is a swap, not a rewrite.

## Why

Three forces drove the call: zero-cost at v0 scale, zero ops surface, and a protocol seam that keeps the cross-platform door open.

**Zero cost, zero key.** MKLocalSearch and CLGeocoder require no API key, no developer-account configuration beyond shipping in an Apple-signed app, and no quota dashboards. Authentication is implicit via the bundle identity. There is no `.xcconfig` injection problem to solve and no token to rotate when it leaks. For v0 (a few hundred users), this is the strict-dominant cost outcome over any paid vendor.

**Zero ops surface.** The alternative paths each carry real ongoing cost:
- **Self-hosted Nominatim** would mean running Postgres + PostGIS with the OSM planet database (~150GB on disk), plus a periodic OSM import job. Tile self-hosting via PMTiles ([ADR 0021](./0021-self-hosted-pmtiles-cloudflare-worker.md)) was cheap because PMTiles is a static blob fronted by a Cloudflare Worker. Geocoding is not analogous — it needs a live, indexed database, which is not a free Worker.
- **Mapbox Geocoding** would mean a public token in the iOS bundle (bundle-ID restriction is the standard mitigation, but adds vendor coupling and a quota to monitor).
- **Google Places** has the same key-management profile as Mapbox plus stricter terms.

**Protocol seam keeps the door open.** Geocoding has exactly one v0 caller: the create-event location picker. Wrapping it behind `GeocodingService` (`search(query:region:) -> [GeocodingResult]`, `reverseGeocode(coord:) -> String?`) means the Android implementation, when it lands, replaces a single file. This is not a "fine for v0" deferral — the abstraction earns its keep on day one because it makes the search field testable with a fake (no real network in unit tests). The Apple-only lock is real but local; it does not propagate.

## Considered alternatives

- **Mapbox Geocoding API.** The strongest contender. 100k temporary-endpoint requests/month free, cross-platform, clean license, monitorable quotas. Rejected for v0 on the basis that we burn meaningfully fewer requests than that across all current users in a year, and the protocol seam means swapping later is mechanical. Re-evaluate when Android lands or when the MapKit license question (below) becomes a friction point.
- **Self-hosted OSM Nominatim.** Cross-platform and ops-controlled, but ~150GB OSM planet DB plus a periodic re-import is the wrong order of magnitude for v0. Rejected.
- **Google Places.** Similar profile to Mapbox with stricter display/attribution terms and tighter caching restrictions. No v0 advantage. Rejected.

## Trade-offs accepted

**MapKit license gray area.** Apple's MapKit terms specify that results from `MKLocalSearch` should be displayed on a MapKit-rendered map. We render with MapLibre ([ADR 0004](./0004-maplibre-native-with-self-hosted-protomaps.md)), not MapKit. The community-consensus reading is:
- Showing results in a list/table is uncontroversial.
- Dropping the resolved coordinate as a pin on a non-MapKit map is a gray area. No public Apple enforcement against this pattern exists, but it is technically out of compliance with a strict reading.
- Scraping MKLocalSearch to build a third-party POI database is clearly forbidden (and not what we are doing).

We accept the gray-area read for v0. The protocol seam means that if Apple ever tightens enforcement, the swap to Mapbox is a single-file change. Re-evaluate before public distribution.

**Apple-only lock.** Collides with [ADR 0003 §Cross-platform plan](./0003-ios-first-native.md) in spirit. Mitigated by the protocol abstraction: Android day-one replaces `AppleGeocodingService` with `MapboxGeocodingService` (or whichever vendor wins re-evaluation) without touching any caller.

**No bulk geocode.** MKLocalSearch is interactive-only — there is no batch endpoint. If we ever want to geocode a CSV of seed events at build time, we will need a second tool. Not a v0 concern.

**Opaque rate limits.** Apple does not publish numbers; throttling is per-device and triggered only by sustained abuse. With 300–500ms debounce on `queryFragment`, this is not a v0 concern, but it means we cannot pre-flight a quota before going wider. Re-evaluate at distribution time.

## Implementation shape

```swift
struct GeocodingResult {
  let title: String        // "Palisades Park"
  let subtitle: String     // "Santa Monica, CA"
  let coordinate: CLLocationCoordinate2D
}

protocol GeocodingService {
  func search(query: String, region: MKCoordinateRegion) async -> [GeocodingResult]
  func reverseGeocode(_ coord: CLLocationCoordinate2D) async -> String?
}

struct AppleGeocodingService: GeocodingService { /* MKLocalSearch + CLGeocoder */ }
```

Caller (location picker) holds a `GeocodingService` reference, not the concrete type. Tests inject a fake.

## Cross-references

- [ADR 0003](./0003-ios-first-native.md) — iOS-first, Android deferred. This ADR's Apple-only lock is consistent with Wave-1 reality.
- [ADR 0004](./0004-maplibre-native-with-self-hosted-protomaps.md) — MapLibre, not MapKit. Source of the license gray area.
- [ADR 0014](./0014-secret-management.md) — this ADR adds zero secrets to the surface.

## Consequences

- The iOS app gains a dependency on `MapKit` and `CoreLocation` frameworks (already linked for the existing map and check-in flows).
- No new secrets, env vars, or vendor accounts.
- Android landing requires writing one new file (`MapboxGeocodingService` or equivalent) and changing the DI wiring; no caller changes.
- Privacy disclosure: place-search queries are sent to Apple's geocoding backend, anonymized via rotating per-app identifiers per Apple's MapKit privacy documentation. To be reflected in the app's privacy policy at distribution time.
- Re-evaluation triggers: Android implementation, public distribution, or any sign of MapKit license enforcement against MapLibre-paired usage.
