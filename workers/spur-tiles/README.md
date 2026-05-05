# spur-tiles Worker

Serves PMTiles vector tiles from R2 as standard XYZ HTTP requests. The
request-shape adapter pattern from
[ADR 0019](../../docs/adr/0019-self-hosted-pmtiles-cloudflare-worker.md):
clients see vanilla `/tiles/{z}/{x}/{y}.mvt`, the Worker translates each
into a range read against `base/california-20260504.pmtiles` in the
`spur-tiles` R2 bucket.

## Routes

- `GET /tiles/{z}/{x}/{y}.mvt` (or `.pbf`) — vector tile
- `GET /healthz` — liveness probe

## Local dev

```sh
npm install
npm run dev
```

Wrangler runs the Worker locally at `http://localhost:8787`. By default it
uses a local R2 simulator; pass `--remote` to hit the real R2 bucket.

## Deploy

```sh
npm run deploy
```

Deploys to `spur-tiles.<account-subdomain>.workers.dev`.

## Bumping the PMTiles archive

When the monthly OSM refresh produces a new archive
(`base/california-YYYYMMDD.pmtiles`), update `vars.PMTILES_KEY` in
`wrangler.jsonc` and redeploy. The old archive can be deleted from R2 once
the new deployment is live.
