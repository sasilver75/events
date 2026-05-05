# Secret management: platform-managed for hosted envs, gitignored `.env.local` for local dev

For hosted environments (staging, eventually prod), all environment variables — secret and non-secret alike — are configured in the deploy platform (Fly secrets for sensitive values, `fly.toml` `[env]` for public ones). The deployed Go process reads them as plain `os.Getenv`; no file is ever materialized in a hosted environment. For local development, a gitignored `server/.env.local` holds local Supabase CLI values; these are not real secrets (they only grant access to a developer's local Docker Postgres) and the file convention is a reasonable convenience tradeoff.

## Why

The naive options both fail:

- **Real secrets in a gitignored `.env`.** Even with `git status` clean, the file lives on dev disks, agent contexts, screen shares, and accidental `git add -A`. Anything that grants access to shared infrastructure (hosted Postgres password, hosted service-role key) doesn't belong on a laptop that might leak it.
- **Everything in the platform secret store, including local dev.** Forces a `fly secrets list --reveal` round-trip every time you run the server locally, and the values being protected — local Docker Postgres credentials — aren't actually secret.

The split rule: **a credential is a secret if it grants access to infrastructure that anyone other than its holder uses.** Local Supabase CLI keys fail that test — they're tied to your local Docker container, signed by a JWT secret your machine generated on `supabase start`, worthless elsewhere. Hosted-staging service-role keys pass it. The two cases get different storage.

## Disposition by variable

| Variable | Local dev | Hosted (staging / prod) |
|---|---|---|
| `DATABASE_URL` | `server/.env.local` (local CLI default `postgres:postgres@127.0.0.1:54322`) | `fly secrets set` — contains pooler password |
| `SUPABASE_URL` | `server/.env.local` (`http://127.0.0.1:54321`) | `fly.toml` `[env]` (public) |
| `SUPABASE_ANON_KEY` | `server/.env.local` (from `supabase status`) | `fly.toml` `[env]` (publishable; Supabase markets this key as client-safe) |
| `SUPABASE_SERVICE_ROLE_KEY` | `server/.env.local` (from `supabase status`) | `fly secrets set` — bypasses RLS, server-only |
| `PORT` | `server/.env.local` | `fly.toml` `[env]` |

iOS gets its config at compile time via `xcconfig` (one per scheme: `Local`, `Staging`, eventually `Prod`). The anon key may be committed in `xcconfig` (publishable by Supabase's design); the service-role key never appears on the client.

## Considered alternatives

- **Doppler / Infisical / 1Password `op run` for local dev.** Rejected for v0 — adds a dependency and a per-dev account for a problem (local CLI defaults) that doesn't need solving. Worth revisiting if the team grows past one person or local config grows genuinely sensitive values.
- **`.env.staging` file (gitignored) for local-pointing-at-staging debugging.** Rejected — same on-disk-leak risks as committing real secrets, and the use case is rare enough that `fly secrets list --reveal` on demand handles it. Reproducing a staging-only bug means fetching credentials interactively, not from a persistent file.
- **All env vars (including local) in the platform secret store.** Rejected — round-trip overhead with no security benefit at this scale.
- **Committed `.env.local.example` template.** Deferred — one solo dev means the live `.env.local` is its own documentation. If a second contributor joins, copy it to `.env.local.example` with values blanked and update the gitignore allowlist.

## Consequences

- **Local secrets are inert by definition.** Bootstrapping a new dev machine is `supabase start && supabase status`, then paste two values into `server/.env.local`. The file's contents are worthless if they leak — that's the whole reason the file is acceptable.
- **Hosted env vars must exist before the server boots.** Once the staging Fly app is created, every variable in the table above must be present (`fly.toml` for non-secret, `fly secrets list` for secret) or startup will panic. Boot-time config validation belongs in the server's startup code.
- **No `.env.staging` anywhere.** If one appears in a worktree or branch, it's a bug — the file shape itself violates this ADR. Delete and route through Fly.
- **Upgrade before distribution.** Production-grade secret management — per-developer secrets, automated rotation, audit logging, break-glass access — is out of scope at v0 personal-project scale. Revisit before opening the app to non-self users, per `PRD-v0.md` §Out of scope.
