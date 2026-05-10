# `public.users` row created by `POST /users/me/profile`, not by trigger

**Status:** Accepted (2026-05-09). Supersedes [ADR 0022](./0022-auth-users-mirror-trigger.md).

The `AFTER INSERT ON auth.users` trigger that mirrored `id` into `public.users` is removed. `POST /users/me/profile` becomes the sole insert path for `public.users`, creating the row with all required profile fields populated atomically at the API boundary. A `profile_required` middleware gate returns 409 to authenticated requests from users who have a JWT but no `public.users` row.

## Why

ADR 0022's load-bearing claim was that "the row exists from the moment the user does." That claim made sense when `public.users` was a single-column shadow of `auth.users.id` — the trigger could populate it with no input, the row was always complete, and any handler joining `public.users` could rely on it being there.

[#88](https://github.com/sasilver75/events/issues/88) extends `public.users` with fields the user has to *type*: `handle`, `display_name`, `dob`, `tos_accepted_at`, `tos_version`, `avatar_path`. Per [PRD-v0 §Account+identity](../../PRD-v0.md) those are NOT NULL — a row without them isn't a meaningful Spur user. A trigger has no input from which to populate them. The choices are:

1. **Trigger creates the row with NULL or placeholder values.** Breaks the NOT NULL constraints, or admits placeholders that lie about user state. Either way "row exists" no longer means "user is signed up" — the invariant ADR 0022 was protecting silently degrades.
2. **Trigger creates a stub in a `users_pending` table; promotion happens on profile POST.** Two-table dance, moves the atomicity gap to a different table boundary, requires PL/pgSQL or extra Go code to manage promotion. Also smells like business logic in PL/pgSQL — see [ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md).
3. **Drop the trigger; `POST /users/me/profile` is the sole insert path.** Restores honest semantics: a `public.users` row exists iff the user has completed signup. The atomicity gap that 0022 worried about is still present in principle (auth.users exists before public.users does), but it is now *intentional, observable, and named*: middleware returns 409 `profile_required` to anyone in that window, and the iOS client resumes signup at the right step. Compare with 0022's gap, which would have been a silent invariant violation if it bit anything.

Option 3 is what ships. The atomicity argument 0022 made hasn't gotten weaker — it's just that "row exists keyed by auth user" is no longer the right invariant to defend, because the row's existence no longer implies what it used to imply.

## Shape

- **Trigger removed.** Schema migration in #88 drops `mirror_auth_user_to_public_trg` and `public.mirror_auth_user_to_public()`. The down migration recreates them so a rollback is reversible.
- **Sole insert path.** `POST /users/me/profile` runs `INSERT INTO public.users (...) VALUES (...) ON CONFLICT (id) DO NOTHING` keyed on the JWT subject. `RowsAffected == 0` returns 409 `profile_complete` (the row already exists; handle is set once per the brief's out-of-scope). A `23505` on `users_handle_key` returns 409 `handle_taken`.
- **`profile_required` middleware.** Authenticated requests except `POST /users/me/profile`, `HEAD /users/handle/{handle}`, and `GET /legal/tos` perform a single `SELECT 1, avatar_path FROM public.users WHERE id = $1` and return 409 `profile_required` if the row is missing. The same query feeds the sibling `avatar_required` gate (one round-trip, two checks).
- **Seed.** `server/db/seeds/seed.go` already INSERTs `public.users` explicitly for curated users; it now carries the new required fields. No new code path for seeds — just additional columns in the existing INSERT.
- **`auth.users` is unchanged.** Supabase Auth still owns it; phone-OTP signup writes to it as before. The break is purely in what `public.users`'s existence implies.

## Considered alternatives

- **Keep the trigger; populate placeholder values (`handle = 'pending_<uuid>'`, `dob = current_date`, …).** Rejected. Placeholders make every downstream join lie. Friend search would surface "pending_..." rows; attendee lists would render placeholder display names; reputation queries would attribute votes to incomplete users. The cure is worse than the gap.
- **Keep the trigger; insert into a `users_pending` table; promote on profile POST.** Rejected. Two-table FK web (which table do `events.host_user_id` and `commits.user_id` point at?), PL/pgSQL promotion logic that ADR 0005 explicitly forbids, and the same observable-state question (does `users_pending` count as "exists"?) just renamed.
- **Lazy upsert in Go middleware (the pre-0022 approach).** Rejected, again. The same per-request cost and reasoning-gap arguments from 0022 still apply. More importantly, lazy upsert can't populate the new NOT NULL fields — middleware has no `handle` to insert. The handler endpoint *is* the only place that has the data.
- **Stay on the trigger; defer the typed columns to a later migration that lands once #89 (liveness) and signup UX are also ready.** Rejected. We'd still hit the same fork the day we add the columns. The only thing deferring buys is more time on the silent-invariant version of the gap.

## Consequences

- **Existence semantics flipped.** A `public.users` row now means "completed signup," not "auth identity exists." Any future code that joins `public.users` benefits — RLS policies in particular get cleaner, because there is no incomplete-user case to filter out.
- **Handler invariant becomes middleware-gated.** ADR 0022's "any handler behind auth middleware may assume `public.users` exists" becomes "any handler behind `profile_required` middleware may assume `public.users` exists, with all NOT NULL fields populated." The invariant is still useful — it just lives one layer deeper.
- **Resume-into-signup is now load-bearing.** If iOS loses local state with a valid JWT (reinstall, device migration), the next authenticated request returns 409 `profile_required` and the client must reopen the signup flow. This is documented behavior, not an error condition. iOS handles 409 `profile_required` and 409 `avatar_required` as resume signals.
- **ADR 0022 is superseded, not retracted.** Its mechanical-mirror reasoning was correct for the schema it was reasoning about. The trigger lived for ~ ten commits. Future "should we add a trigger for X?" questions should still consult 0022 for the boundary.
- **`docs/agents/domain.md` and `server/db/README.md` will need updates.** Both currently reference the trigger as a live mechanism. The PR for #88 updates them.
- **Distribution gate.** Pre-distribution, revisit: alerting on `profile_required` 409 spikes (could indicate a real signup-flow regression vs. expected resume traffic), and whether the resume-into-signup path needs a server-driven step indicator (today iOS infers the step from the 409 code).
