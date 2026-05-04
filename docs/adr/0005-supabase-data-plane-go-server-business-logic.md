# Supabase as data plane, Go server for business logic

v0's backend is split: **Supabase** provides the managed data plane (Postgres+PostGIS, Auth, Realtime, Storage), and a **Go HTTP server** owns business logic for writes that carry real rules (Commit, Withdraw, create event, check-in, rate, friend-request, report). Pure-Supabase (Edge Functions only) and fully-custom (Go-from-scratch including auth/realtime/storage) are rejected.

## Why

Two forces pulled in opposite directions.

The product genuinely benefits from Supabase's managed primitives. PostGIS is the right shape for a geo-heavy app, Realtime over Postgres logical replication fits the live pin-count and chat use cases directly, RLS expresses privacy-by-default elegantly, and managed Auth/Storage removes weeks of plumbing.

But the v0 author wants a real backend codebase to own and learn in (Go). Supabase Edge Functions are real code but their shape — short-lived HTTP handlers, per-invocation timeouts, no long-running workers, scattered cron-triggered jobs — does not satisfy the "I want to write and deploy a server" itch. And business logic written purely in PL/pgSQL triggers and DB functions is unappealing both ergonomically and in reviewability.

The split path resolves both. Supabase handles the parts that have nothing to do with our domain (running Postgres, phone OTP, WebSocket fan-out, S3-compatible storage). The Go server owns the parts that carry our actual rules.

## Shape

- **iOS → Supabase directly** for: Auth (phone OTP), Realtime subscriptions (live pin counts, chat messages), Storage uploads (selfie, event photos), simple reads (read-only views with RLS).
- **iOS → Go server** for: Commit/Withdraw, create event, check-in (geofence-validated server-side), submit rating, friend request, report, anything with non-trivial rules.
- **Go server → Supabase Postgres directly** via `pgx` and the `DATABASE_URL` — no Supabase client SDK on the server side.
- **DB triggers and pg_cron used sparingly.** Acceptable for low-logic mechanical concerns (e.g., a webhook that pings the Go server when an event row's commit count crosses its threshold). Business rules do not live in the database.
- **RLS is defense-in-depth**, not the only line of defense. The Go server enforces rules first; RLS catches anything that slips through and protects direct iOS→Supabase reads.

## Considered alternatives

- **Pure Supabase (Edge Functions for all logic).** Rejected: doesn't scratch the "real backend" itch, and the cron + worker story is awkward enough that we'd hit it within v0.
- **DB-heavy Supabase (PL/pgSQL triggers and functions for business rules).** Rejected outright: the author has no appetite for writing serious business logic in PL/pgSQL, and AI assistance is weaker there than for TypeScript or Go.
- **Fully custom Go backend (no Supabase).** Rejected for v0: rebuilding auth, realtime, and managed Postgres absorbs 6–10 weeks of calendar time with zero product upside. Worth revisiting only if Supabase becomes a constraint.
- **Firebase.** Rejected: Firestore fights the relational/geo shape of this app, and there is no escape hatch comparable to Supabase's Postgres-underneath posture.

## Consequences

- One more service to operate (the Go server). Deploy target chosen separately — Fly.io or Railway are the leading candidates for v0 ergonomics.
- Two SDK surfaces on iOS: Supabase's Swift client (Auth, Realtime, Storage, simple reads) and our own thin HTTP client for Go server endpoints.
- Authorization story is split. Supabase Auth issues the JWT; the Go server validates it (Supabase publishes JWKS) and uses the user ID for authorization. RLS uses the same JWT for direct-Supabase reads.
- Migrations live with the Go server (e.g., `pressly/goose` or `golang-migrate`) and run against the Supabase Postgres. Supabase's own migration tooling is not the source of truth.
- We deliberately do not run our own Postgres or our own WebSocket server in v0. If we ever need to leave Supabase, the Postgres data and SQL schema port directly; auth and realtime would need rebuilds.
- Push notification fan-out, scheduled tasks (β tip-deadline expiry, post-event rating prompts, reputation recompute), and third-party integrations (APNS, SMS via Supabase Auth → Twilio) all live in the Go server.
