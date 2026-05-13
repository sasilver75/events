# Mirror auth.users → public.users via a Postgres trigger

**Status:** Superseded by [ADR 0025](./0025-public-users-row-created-by-profile-post.md) (2026-05-09). The trigger was correct while `public.users` only mirrored `id`; once profile fields landed, the row's existence stopped implying signup completion and the trigger no longer earned its keep.

A Postgres trigger on `auth.users` mirrors the row's `id` into `public.users` inside Supabase Auth's own transaction. The Go server does **not** perform a lazy upsert on every authenticated request. This refines [ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md)'s mechanical-trigger carve-out by recording what counts as "mechanical" and what does not.

## Why

Two `users` tables are in play. Supabase Auth owns `auth.users` and inserts into it when a phone OTP verifies (or any other Auth flow lands a new identity). The Spur domain owns `public.users`, keyed by the same UUID, and is the FK target for `events.host_user_id` and `commits.user_id`. Bridging the two needs a write to `public.users` whenever `auth.users` gains a row.

Three forces shaped the choice.

The first is **atomicity of identity creation**. Supabase Auth runs in its own process (`GoTrue`) with its own transaction. The Go server has no way to extend GoTrue's transaction over the wire — by the time a JWT lands in our middleware, GoTrue's commit is already on disk. The only place where a `public.users` insert can be made part of the *same* transaction as the `auth.users` insert is inside Postgres itself, via a trigger. Anything outside Postgres (Go middleware, GoTrue HTTP hooks, an explicit `POST /me/init` from iOS) creates a window where `auth.users` exists and `public.users` does not. That window is small in expected operation but large enough to produce real bugs — most acutely, future RLS policies that join `public.users` would silently exclude users who haven't yet hit our server. The trigger eliminates that bug class by construction.

The second is **what ADR 0005 actually says about triggers**. ADR 0005 line 20: *"DB triggers and pg_cron used sparingly. Acceptable for low-logic mechanical concerns. Business rules do not live in the database."* Mirroring `id` from `auth.users` to `public.users` has zero business logic — no conditionals beyond `ON CONFLICT DO NOTHING`, no domain rules, no derived values, no decision points. It is mechanical the same way "denormalize a count" is mechanical. The earlier framing in #7's closeout and `server/db/README.md` ("no Postgres trigger mirrors auth.users into public.users") read ADR 0005 as a blanket prohibition; the ADR doesn't say that. This ADR records the calibrated reading.

The third is **per-request cost**. The lazy-upsert alternative paid one `INSERT ... ON CONFLICT DO NOTHING` round-trip on every authenticated request. That cost is cheap in absolute terms (Postgres short-circuits on the PK index check) but pure overhead for the >99% of requests where the row already exists. Moving the insert into the once-per-user signup path removes that overhead entirely.

## Shape

- **Trigger.** `AFTER INSERT ON auth.users FOR EACH ROW`, calling a `plpgsql` function that runs `INSERT INTO public.users (id) VALUES (NEW.id) ON CONFLICT (id) DO NOTHING`. `ON CONFLICT` keeps the trigger idempotent against any path that might already have inserted (e.g., the curated-event seed runner, which still upserts `public.users` explicitly for its own clarity).
- **Migration.** Lands as `0006_mirror_auth_users.up.sql` / `.down.sql` under `server/db/migrations/` per [ADR 0017](./0017-migration-tool-golang-migrate.md). The down migration drops both the trigger and the function.
- **Permitted mechanical scope.** Eligible: row-mirroring across schemas keyed by the same identifier; denormalized count maintenance; `pg_notify` emission. Not eligible: anything that consults a second column, applies domain rules, computes derived business state, or branches on values beyond `ON CONFLICT`. When in doubt, write it in Go.
- **Go middleware.** Validates the JWT, attaches the user UUID to the request context, and returns. No DB write. Handlers downstream may assume `public.users(id)` exists for the authenticated user.
- **Curated-event seed.** Continues to upsert its own `public.users` row explicitly (see `server/db/seeds/seed.go`). The trigger and the explicit upsert agree under `ON CONFLICT DO NOTHING`; both being present makes the seed runnable in isolation against a database where the trigger has not yet been applied (e.g., during the migration that introduces the trigger).

## Considered alternatives

- **Lazy upsert in Go middleware (the originally-shipped approach).** Rejected. Atomicity gap is real even if it doesn't bite anything in v0; per-request cost is overhead that compounds; the rule "any handler behind auth middleware can assume `public.users` exists" is harder to reason about than "the row exists from the moment the user does."
- **HTTP webhook hook (`auth.hook.before_user_created` over HTTP).** Rejected. Adds a synchronous dependency on the Go server during signup — if the server is down, signup itself breaks. Worse availability story than the trigger, which runs in-process with the same Postgres GoTrue is already writing to.
- **`auth.hook.before_user_created` configured as a `pg-functions://...` PL/pgSQL hook.** Rejected on the same atomicity grounds the trigger satisfies, but with worse ergonomics: the hook fires before insert and has a richer contract (it can reject the user creation), which is more surface area than a mirror needs. A plain `AFTER INSERT` trigger is the minimum-power tool for the job.
- **Eager `POST /me/init` from iOS after OTP verify.** Rejected. Two round-trips instead of one. New failure mode (signup succeeds, init drops) that requires retry logic on the client. Strictly worse than the trigger.
- **Drop `public.users` entirely; have FKs target `auth.users(id)`.** Rejected. `auth.users` is owned by Supabase and we cannot add columns to it (`display_name` and future profile fields land in `public.users`). Decoupling is correct; we just need the link populated atomically.

## Consequences

- **One trigger lives in `public` migrations.** It is the only PL/pgSQL the project ships at v0. The mechanical-mirror scope, defined above, is the boundary — additions go through a fresh ADR, not "while we're in there" expansion.
- **The Go middleware is simpler.** No DB pool dependency on the auth path, no per-request upsert, no hidden invariant for handlers to remember. Middleware does JWT verification only.
- **Reverses a public stance.** Issue #7's closeout, `server/db/README.md`, and the "no business logic in PL/pgSQL" auto-memory all asserted "no trigger mirrors auth.users into public.users." Each is updated when this ADR lands; the memory's body now references this ADR's mechanical-mirror exception so future sessions don't repeat the over-strict reading.
- **Triggers are easy to forget about during debugging.** "Why did this row appear?" → "a trigger fires on auth.users insert." Mitigation: the migration sits in version control, the trigger appears in `server/db/README.md`, and this ADR is the citable record. If we ever need to debug why a row doesn't appear, `\d auth.users` shows the trigger.
- **Distribution gate.** Before shipping to a real audience, revisit: alerting on trigger failure (today a trigger error would surface as a signup 5xx with no special instrumentation), and migration-shape review for any subsequent triggers that try to claim the same mechanical-mirror exception. The boundary is firm at v0; expansion needs justification.
