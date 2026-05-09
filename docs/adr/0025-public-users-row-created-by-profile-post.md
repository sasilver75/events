# `public.users` row is created by `POST /users/me/profile`, not by a trigger

The Postgres trigger that mirrored `auth.users → public.users` (introduced in migration 0006 under [ADR 0022](./0022-auth-users-mirror-trigger.md)) is dropped. The Go server's `POST /users/me/profile` endpoint becomes the **sole insert path** for `public.users`, creating the row with all required profile fields populated atomically at the API boundary. This ADR supersedes ADR 0022.

## Why

ADR 0022 made a calibrated argument: when `public.users` only mirrors `id` from `auth.users`, the trigger keeps row existence atomic with Supabase Auth's own transaction, costs nothing per request, and adds zero business logic — pure mechanical mirroring, well within the boundary ADR 0005 carves out for triggers.

That argument held while `public.users` had only `(id, created_at, display_name)` and `display_name` was nullable. Issue #88 changes the table's contract. Real signup completeness — handle (unique, format-checked), `handle_display`, `display_name`, DOB (≥ 18), ToS attestation (timestamp + version) — lands as `NOT NULL` columns the user has to type in. The trigger cannot populate any of them; it would have to insert a row that violates its own NOT NULL constraints, or every NOT NULL column would have to drop to nullable and re-tighten later.

This collapses the original atomicity argument. The trigger keeps the **row's existence** atomic with `auth.users`, but the row is now functionally empty until `POST /users/me/profile` lands the real fields. Anything reading `public.users` between OTP verify and profile POST sees a placeholder. The atomicity gap hasn't been closed — it's just moved from "row missing" to "row exists but is empty."

Given the gap moves either way, the right move is to put the `INSERT` where it logically belongs: at the API boundary where the profile data is supplied. One write, atomic with the data, validated by the same handler that enforces the business rules (handle format, DOB threshold, ToS version match). Removing the trigger also removes the only PL/pgSQL the project ships, simplifying the mental model under [ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md): writes that carry rules go through Go, full stop.

## Shape

- **Trigger dropped.** Migration 0016 drops `mirror_auth_user_to_public_trg` and `public.mirror_auth_user_to_public()`. Down migration recreates them (so the migration is reversible against any environment that ran 0006).
- **`POST /users/me/profile` is the sole insert path.** The handler runs `INSERT INTO public.users (id, handle, handle_display, display_name, dob, tos_accepted_at, tos_version) VALUES (...) ON CONFLICT (id) DO UPDATE SET ...`, keyed on `auth.uid()` from the JWT subject. Idempotent at the JWT subject so signup-flow retries are safe.
- **Profile-required middleware.** The Go middleware previously validated the JWT and assumed `public.users(id)` existed for any authenticated request (per ADR 0022). With the trigger gone, that invariant only holds *after* `POST /users/me/profile`. A new middleware layer runs one `SELECT 1 FROM public.users WHERE id = $1` per request and returns `409 {error: "profile_required"}` if the row is missing. Endpoints exempt from this check are exactly the three the in-flight signup needs to call: `POST /users/me/profile`, `HEAD /users/handle/{handle}`, `GET /legal/tos`.
- **iOS resume semantics.** A 409 `profile_required` from any authenticated endpoint is the iOS app's signal to reopen the signup flow at the right step. JWT alone is no longer "you're in"; the profile row's existence is.
- **RLS implication, deliberate.** RLS policies that join `public.users` (friend search, attendee lists, etc.) silently exclude users who haven't completed signup. This is correct behavior — a user who has verified their phone but hasn't accepted ToS or stated a DOB shouldn't appear in friend search or be reachable by handle. Documented here so a future reader doesn't read the exclusion as a bug.

## Considered alternatives

- **Keep the trigger; let it insert with placeholder NOT NULL values.** Rejected. Either the trigger generates a placeholder handle (collides with the user's eventual chosen handle, or violates the unique constraint, or requires a reserved-handle ban list), or the columns drop to nullable and the schema lies about the contract. Both are worse than just inserting once with the real data.
- **Keep the trigger; let it insert into a sparse staging table that the profile POST promotes into `public.users`.** Rejected. Two sources of identity truth, two-step promotion, and a new failure mode (promotion-time conflict). The atomicity argument never asked for two tables.
- **Auth hook (`auth.hook.before_user_created`) over HTTP that calls into the Go server.** Rejected for the same reasons ADR 0022 rejected it: signup becomes synchronously dependent on the Go server being up, and the hook fires before insert anyway, so it can't see the eventual `auth.users.id`.
- **Defer the schema change; keep ADR 0022 intact and add profile fields to a separate `public.profiles` table.** Rejected. The natural shape is one user → one profile row, and the FKs (events.host_id, friendships, commits) all already point at `public.users(id)`. Splitting them creates a join on every read for no atomicity benefit.

## Consequences

- **No PL/pgSQL ships at v0.** ADR 0022's mechanical-mirror exception is now empty in practice. The "no business logic in PL/pgSQL" rule from CLAUDE.md and the auto-memory return to a flat reading: triggers are eligible only for denormalized counts and `pg_notify` emission, and neither has a v0 use case yet.
- **One additional Postgres round-trip per authenticated request** for the profile-required check. Acceptable at v0 scale; the lookup is a PK probe in the buffer cache. Distribution gate: revisit if request volume grows. Caching the row's existence in the JWT (e.g. via a custom claim populated by `POST /users/me/profile`) is a clean upgrade path that doesn't require the trigger to come back.
- **iOS gains a resume path.** "Valid JWT but no profile" is a reachable state (user closes the app between OTP verify and ToS accept, app crashes mid-handle entry, etc.). The 409 `profile_required` contract makes the resume deterministic: the app fetches whatever signup step is missing and re-runs from there.
- **ADR 0022 is superseded.** Its decision stands historically — the trigger was the right call for the schema as it existed then — but it no longer governs the codebase. Migration 0006 stays in version control as the introducing migration; migration 0016 is the symmetric drop.
- **Distribution gate.** Before shipping to a real audience: confirm the profile-required middleware doesn't leak a tractable user-enumeration oracle (it shouldn't — it only returns yes/no for the caller's own row), and confirm the resume path on the client survives every relevant Auth state transition (sign out, token refresh, app reinstall).
