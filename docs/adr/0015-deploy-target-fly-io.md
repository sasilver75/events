# Deploy target: Fly.io

The Go server deploys to **Fly.io**, with the v0 instance pinned to region **`sjc`** (San Jose). Railway and self-hosted (a single VPS) are rejected for v0.

## Why

[ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md) leaves the deploy target deliberately open and flags two forces: low Go→Supabase-Postgres latency and v0 ergonomics. Two more forces apply.

The first is **regional control**. Supabase Postgres runs on AWS; the Go server's request-path latency to Postgres is a function of how close the two regions are. The expected user base is California-heavy (SoCal + NorCal), which makes AWS `us-west-1` (N. California) the natural Supabase region and Fly's `sjc` the natural colocation. Fly exposes region pinning at the machine level — `fly.toml` names the region directly and `fly machines` operates per-region — which makes "keep the Go server next to Postgres" a one-line decision rather than an inference.

The second is **the learning surface**. ADR 0005 is explicit that v0 exists in part to scratch the "I want to write and deploy a server" itch. Fly's model — Dockerfile, `fly.toml`, machines, volumes, regions, health checks visible in CLI — is closer to operating a server than Railway's higher-level platform abstraction. Railway is faster to first-deploy and has a friendlier dashboard, but the abstractions hide more of what we're trying to learn.

At v0 scale (~hundreds of users) either platform's free/hobby tier is sufficient and either's reliability is fine. The decision is driven by region control and pedagogy, not capacity or cost.

## Shape

- **Region:** `sjc` (San Jose) — closest Fly region to AWS `us-west-1` (Supabase's expected home). Resolved jointly with [Issue #4](https://github.com/sasilver75/events/issues/4); if Supabase ends up in a different region, this ADR is amended rather than the Fly app re-homed silently.
- **App config:** `/server/fly.toml` checked into the repo. Auto-generated `Dockerfile` is fine for v0; we own it and can hand-edit later.
- **Machines:** start with one machine on the `shared-cpu-1x` / 256 MB tier. Auto-stop disabled — a cold-start delay on the first request after idle is a poor fit for a discovery app.
- **Env vars and secrets:** the split between `fly.toml [env]` (public values) and `fly secrets set` (sensitive values) is governed by [ADR 0014](./0014-secret-management.md). `PORT` lives in `[env]`.
- **Healthcheck:** `GET /healthz` wired into `fly.toml`'s `[[services.http_checks]]` so Fly considers a machine unhealthy and pulls it out of rotation if the route stops returning `200`.
- **Logs:** `fly logs` for tailing; deploy logs accessible via `fly releases`. Production log retention left at Fly defaults (30 days at time of writing) — no separate aggregator at v0.
- **Deploy command:** `fly deploy` from `/server/`, documented in `/server/README.md`.

## Considered alternatives

- **Railway.** Rejected: weaker regional control (multi-region is newer and the surface is thinner) and the higher-level abstraction hides the parts we're deploying to learn from. The first-deploy ergonomics are real but the gap shrinks once Fly is set up once.
- **Self-hosted single VPS (Hetzner, DigitalOcean droplet, etc.) with systemd or Docker Compose.** Rejected for v0: more learning surface but absorbs days of plumbing (TLS, restart policy, deploy pipeline, log shipping) before the first endpoint serves traffic. Worth revisiting if Fly's pricing or constraints become real.
- **AWS (ECS, App Runner, Elastic Beanstalk).** Rejected: same managed-platform shape as Fly with a steeper config surface and no regional advantage over Fly `sjc` ↔ Supabase `us-west-1` colocation.
- **Render.** Rejected: similar shape to Railway with the same regional weakness.
- **Cloudflare Workers / Vercel Functions / Lambda.** Rejected: serverless cold starts and per-invocation timeouts fight the long-lived `pgx` connection pool the Go server wants, and reintroduce the Edge-Functions awkwardness ADR 0005 already declined.

## Consequences

- **Region change is non-trivial but not painful.** If the Supabase region ends up elsewhere, moving the Fly app means a new app or a region change plus a redeploy — not a platform migration. The decision is reversible enough that we don't need to delay shipping for it.
- **Cost at v0 is near-zero.** A single `shared-cpu-1x` machine on Fly's hobby tier sits inside the free allotment for typical v0 traffic.
- **No multi-region.** v0 runs in one region; reads and writes both go through `sjc`. Out-of-region users (e.g., East Coast testers) eat the cross-country round trip. Acceptable at v0 scale; revisit if real traffic shows it.
- **Fly outages page us, not Railway.** v0's uptime is now coupled to Fly's. Their reliability is fine but not zero-incident; documenting the Status page (`status.flyio.net`) in `/server/README.md` is part of the rollout.
- **Distribution gate.** Before shipping to a real audience, revisit: capacity (autoscaling rules, machine sizing), multi-region if user base broadens, and a real log/metrics destination beyond `fly logs`. None of those earn their keep at v0 scale.
