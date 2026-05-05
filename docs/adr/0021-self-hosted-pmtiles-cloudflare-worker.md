# Self-hosted PMTiles via Cloudflare Worker in front of R2

**Status:** Accepted (2026-05-04). Supersedes the "Wave-1 deferral: hosted tiles before self-hosting" section of [ADR 0004](./0004-maplibre-native-with-self-hosted-protomaps.md).

v0 serves map tiles from a single `.pmtiles` file on Cloudflare R2, fronted by a Cloudflare Worker running `go-pmtiles serve`. The Worker translates incoming XYZ tile requests (`/tiles/{z}/{x}/{y}.mvt`) into HTTP range reads against the PMTiles file in R2 and returns a complete tile blob. The iOS client sees a vanilla XYZ tile source and holds no upstream API key. Direct client-to-R2 range requests and the previously planned Wave-1 hosted-tier deferral are rejected.

## Why

Three forces drove the call: avoiding an embeddable API key, keeping latency low, and minimising operational surface.

**No upstream key in the binary.** Hitting Protomaps' hosted tier from the iOS client requires shipping the Protomaps API key in the app bundle. `.xcconfig` injection keeps the key out of git but does not stop binary extraction — anyone with the `.ipa` can pull the key with `strings`. Vendor-side bundle-ID restriction is the standard mitigation for this class of API ([see "How people deal with shipping API keys"](#cross-references)), but it is not confirmed available on Protomaps' commercial tier. Self-hosting eliminates the question entirely: there is no upstream key to leak, because tile requests terminate at our Worker against our R2 bucket.

**Worker as a request-shape adapter unlocks edge caching.** This is the load-bearing technical insight. PMTiles is a single archive file accessed by HTTP byte-range requests. If the iOS client reads R2 directly via range requests (`Range: bytes=12345-12567` against `la.pmtiles`), HTTP caches struggle: cache keys are URLs, not byte ranges, and behaviour across CDNs varies (Cloudflare requires Cache Reserve or similar paid features for reliable range caching). The Worker transforms the request shape into "GET `/tiles/14/2849/6541.mvt` returning a complete tile blob." Each tile becomes a unique URL with a complete response — exactly what HTTP caches are designed for. Cloudflare's free edge cache then sits in front of the Worker automatically, achieving near-100% steady-state hit rates because tiles are immutable.

Latency comparison (LA user, LA-region tiles):

| Path                          | Cold tile | Warm tile (edge cache hit) |
| ----------------------------- | --------- | -------------------------- |
| Worker → R2 (this ADR)        | ~30–60ms  | ~10–20ms                   |
| iOS direct range-read → R2    | ~50–100ms | ~50–100ms (no edge cache)  |
| Protomaps hosted free tier    | ~50–150ms | ~50–150ms                  |
| Go server (Fly LAX) → R2      | ~80–150ms | n/a (no edge cache)        |

The Worker path is the fastest in steady state, not despite the extra hop but *because* the extra hop transforms the request into a cache-friendly shape.

**Operational surface stays tiny.** `go-pmtiles serve` is the official Protomaps tool, deploys as a Worker with a few lines of config, and has no state of its own. R2 storage for an LA-region extract is roughly $0.003/month; egress is free. Cloudflare Workers' free tier (100k requests/day) covers v0's expected volume comfortably — and Cloudflare's edge cache absorbs the bulk of requests before they reach the Worker.

## Considered alternatives

- **Hosted Protomaps free tier (the original ADR 0004 Wave-1 plan).** Rejected: the API-key-in-binary problem is unsolvable without server-side mitigation, and bundle-ID restrictions on Protomaps' commercial tier are unconfirmed. Also forces a planned re-do once we paid-tier or scale, which is more total work than just self-hosting from day one.
- **Direct iOS → R2 range requests (no server in path).** Rejected: loses edge caching as described above. Also requires either a third-party PMTiles iOS plugin (quality concerns) or rolling our own (~2–4 days, plus ongoing maintenance of binary-format parsing). The latency win does not materialise because R2 origin reads are slower than Worker-fronted edge cache hits.
- **Go server on Fly.io as the tile proxy.** Rejected: an extra ~70ms RTT to a single region (LAX best case), no edge cache without further work, and now the map breaks if the Go server is down. The Worker has none of these properties — it is closer to users, automatically cached, and doesn't couple map rendering to our application server.
- **Mapbox or Google Maps SDK hosted tiles.** Already rejected by ADR 0004 for cartography and pricing reasons; the key-handling story is also worse for Mapbox at scale.

## Cross-references

- [ADR 0004](./0004-maplibre-native-with-self-hosted-protomaps.md) — MapLibre Native + Protomaps + R2 primitives. This ADR supersedes only its Wave-1 hosted-tier deferral, not the rest.
- [ADR 0014](./0014-secret-management.md) — secret management posture; this ADR removes the Protomaps API key from the secret-management surface entirely.

## Consequences

- The iOS app holds no Protomaps or tile-service credentials. Map rendering depends only on the Worker URL being reachable.
- Tile pipeline ops surface: regenerate the `.pmtiles` extract from OSM data periodically (monthly cadence is fine, per ADR 0004). One command via `go-pmtiles extract`, then `aws s3 cp`-style upload to R2.
- Glyphs (font PBFs) and sprites (icon JSON + PNG) used by the Protomaps style are also hosted on R2 as static files — no Worker translation needed for those, since they are already small atomic resources.
- The Cloudflare Worker becomes a piece of infrastructure we own. It is small (the `go-pmtiles serve` binary plus a few lines of binding config) and stateless, but it is in the critical path for map rendering. If the Worker goes down, the map goes down — same blast radius as if a tile vendor went down.
- Cloudflare account becomes a v0 dependency alongside Supabase and Fly. Three-vendor surface, all with generous free tiers at v0 scale.
- Free-tier Worker quota (100k requests/day) is comfortable for v0, but worth monitoring once real users browse the map. Edge cache hits do not count against the Worker quota — only cache misses invoke the Worker — so headroom is large.
- Migration door: if Cloudflare Workers ever stops fitting (cost, latency, vendor risk), the same `go-pmtiles serve` binary runs anywhere. We could move to a Fly app, a Lambda, or a self-hosted box with no client-side changes.
