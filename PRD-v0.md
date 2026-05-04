# Spontaneous Events — PRD v0

> Companion to `CONTEXT.md` (vocabulary + structural commitments) and `docs/adr/` (locked decisions).
> This document is product-facing: it states **what we're building and why**, not how.
> A separate technical design doc will follow.

---

## Problem statement

Mid-sized cities — Los Angeles in particular — are full of people who would like to spend time around other people doing low-friction activities (pickup sports, beach hangs, yard games, dinners, coffee meetups), but who currently default to staying home because:

- **Group chats only surface their friends' availability**, not the strangers nearby who are also free and looking.
- **Meetup, Eventbrite, and Facebook Events are scheduled-and-formal**, surfacing recurring interest groups and ticketed events — not "I'm bored tonight, who's playing tag in Echo Park in two hours?"
- **No existing app makes it feel safe to commit to an event with strangers** — there's no way to evaluate who else will be there, no way to confirm the event will actually fire, and no way to leave gracefully if you change your mind without ghosting.

The result is that LA's social density gets wasted nightly: thousands of people who would happily meet up with strangers for a casual shared activity never connect, because the coordination problem is unsolved.

## Solution

A location-based, mobile-first social app that surfaces **short-horizon (≤72hr) gatherings between strangers** on a map of the city. Two event shapes:

- **Hosted Events (α):** familiar Meetup-style. One person proposes, runs, and is accountable.
- **Seeded Events (β):** a novel mechanic. A user *seeds* an idea ("Tag in Echo Park, 6 needed"); the event isn't real until N strangers Commit, at which point it Tips and becomes binding. The threshold turns flake-prone "maybe" intent into honest commitment.

Both shapes show up live on a tilted 2D map of the city with rich, animated pins. Users can see Events filling in real time ("4/6 — 1 from Tipping!"), Commit with one tap, and watch threshold-events crystallize into real plans.

The wedge — the thing nothing else does well — is **spontaneous coordination between strangers** in the next 72 hours, with binding commitment and graceful off-ramps that make stranger-meetups feel safe to try.

## Target user (v0)

Adults (18+) in Los Angeles who:

- Have unstructured social time (evenings, weekends) they'd like to fill more reliably.
- Are open to meeting strangers in casual, low-stakes settings.
- Already use other apps (Strava, Hinge, Meetup) but find none of them serve "I want to do something near me, soon, with whoever's around."

The product is *not* targeting: people seeking deep recurring community (Meetup serves that), people seeking ticketed entertainment (Eventbrite serves that), people seeking 1:1 dating (Hinge/Bumble serve that), or minors.

---

## User stories

### Onboarding

1. As a new user, I want a brief preview of what the app feels like *before* signing up, so that I can decide whether the product is worth the friction of creating an account.
2. As a new user, I want a verification process that takes under two minutes, so that I don't abandon during sign-up.
3. As a new user, I want my live selfie used both for verification and as my default avatar, so that I'm not asked to do the same kind of work twice.
4. As a new user, I want permission requests (location, notifications) to come at moments where their value is obvious, so that I don't deny them out of generic suspicion.
5. As a new user, I want a light tutorial that points out a few key affordances and then gets out of my way, so that I can start exploring.

### Discovery

6. As a user, I want to see Events near me on a map of the city, so that I can find things to do without typing.
7. As a user, I want to filter Events by time horizon (right now → 72hr), so that I can plan tonight or this weekend with the same surface.
8. As a user, I want Events to have visible categories (Sports, Food, Outdoors, etc.), so that I can recognize what kind of activity each pin represents at a glance.
9. As a user, I want the visual treatment of pins to reflect temporal urgency (Live now > soon > later > muted), so that my eye is drawn to events I can act on immediately.
10. As a user, I want to see Events that are filling in real time (3/6, 4/6, 5/6), so that I feel the momentum and excitement of strangers committing.
11. As a user, I want to see which of my Friends are Attending an Event when I look at its pin, so that I can use friend signal in context — without being able to browse my friends' upcoming plans.
12. As a user, I want to see which Events are full ("8/8") at a glance, so that the map continues to feel populated and active.

### Hosting and Seeding

13. As a Host, I want to create an Event with a fixed start time, location, category, description, and Cap, so that I can run a familiar Meetup-style gathering.
14. As a Seeder, I want to create an Event with a Tip threshold (the minimum Commits required for the Event to become real), so that I can propose a coordination without taking on Host responsibility if no one bites.
15. As a Seeder, I want to optionally set a Cap (the maximum Commits) above the Tip threshold, so that flakes can be replaced by new Commits without an unbounded crowd.
16. As a Seeder, I want the Event to automatically Cancel if it doesn't reach the Tip threshold by a deadline (default: 1hr before start), so that stale "almost happened" events don't pollute the map.
17. As a Seeder, I want to **not** be able to cancel my own Event after creation, so that strangers who Committed in good faith aren't yanked at my whim.
18. As a Host, I want to be able to cancel my Event with reputation cost that scales with how late the cancellation is, so that emergencies are tolerable but flakiness has a real cost.
19. As a Host or Seeder, I want to attach a gating rule to my Event (rep ≥ X, friends only), so that I can shape who shows up.
20. As a Host or Seeder, I want my Event's exact location hidden from non-Attendees by default, with an opt-out for genuinely public places, so that strangers can't crash and stalkers can't observe.
21. As a Host or Seeder, I want my Event to be discoverable at neighborhood-level on the map even when full, so that the city looks active.

### Committing and Withdrawing

22. As an Attendee, I want to Commit to an Event with one tap, so that joining feels frictionless.
23. As an Attendee, I want to Withdraw my Commit at any time without a guilt-trippy interface, so that releasing my spot feels strictly preferable to ghosting.
24. As an Attendee, I want to know that my Withdrawal will count differently depending on when it happens (early = clean; late on a β-Event = a Flake; not Withdrawing and not Showing up = Ghost), so that I can choose honestly.
25. As an Attendee on a β-Event, I want my Withdrawal to be reflected immediately in the count and to potentially re-open the slot to nearby users, so that flakes turn into opportunities for someone else.
### Privacy and presence

26. As a user, I want never to be able to browse another user's upcoming events or commit history, so that the platform isn't a stalking surface.

### At the Event

27. As an Attendee, I want to check in at the Event when I arrive by tapping a button, so that my Show is recorded.
28. As an Attendee, I want check-in to be gated by my GPS being near the Event pin, so that no one can defraud the system by checking in remotely.
29. As an Attendee on an Event that has Tipped (β) or been created (α), I want a group chat with the other Attendees, so that we can coordinate logistics ("I'm by the picnic table, red shirt").
30. As an Attendee, I want the chat to remain accessible for 24 hours after the Event is Done, so that we can share photos and make plans for next time, before it archives.

### After the Event

31. As an Attendee, I want a prompt within ~30 minutes of the Event ending to rate my experience, so that the system gets honest feedback while it's fresh.
32. As an Attendee, I want a prompt to add the people I just met as Friends after the Event, so that the network effect of stranger-to-friend conversion has the highest-yield surface.
33. As an Attendee, I want the option to flag a fellow Attendee as "would not attend with again" anonymously, so that I can warn the system without confronting them.

### Reputation and trust

34. As a user evaluating whether to Commit to an Event, I want to see the Host's Host rep (for α) or be reassured by the Tip threshold mechanic (for β), so that I have signal on the trustworthiness of the gathering.
35. As a Host, I want to see prospective Attendees' Attendee rep so I can decide whether to set a rep gate, so that I can shape the room's reliability.
36. As a new user with no rep yet, I want to be visibly *Unrated* rather than low-rep, so that I'm not punished for being new.
37. As any user, I want my flags to be anonymous to the people I'm flagging, so that I can speak honestly without fear of retaliation.
38. As a user who experiences a serious safety incident (harassment, threats, assault), I want a clearly distinct **Report** path that goes to a human moderation queue with severity tiers, so that the system treats it as more than a rating signal.

### Friends

39. As a user, I want to send and accept Friend requests bidirectionally, so that the friend graph is opt-in on both sides.
40. As a user, I want to DM Friends outside any specific Event chat, so that we can coordinate plans privately.
41. As a user, I want to be eligible for "friends only" gated Events created by my Friends, so that intimate gatherings are possible on the platform.

### Notifications

42. As a user, I want unmissable notifications for Events I've Committed to (starts soon, slot opened, cancellation), so that I never miss a real commitment.
43. As a user, I want notifications about milestones on my Committed Events (Tipped, slot opened) by default, so that I feel the momentum.
44. As a user, I want to opt *into* discovery notifications rather than have them on by default, so that I'm not pushed into churn by overzealous surfacing.

### Surfaces

45. As a user, I want a "Your Events" tab that lists my upcoming and recent Commits, so that I can see at a glance what I'm doing.
46. As a user, I want tapping an Event in the list to pan the Map to its pin and open its detail, so that the Map remains the canonical view rather than a parallel one.

---

## Functional requirements

The following capabilities define the platform. Each is grounded in the design conversation captured in `CONTEXT.md`.

**Account and identity**
- Sign-up requires phone verification, live in-app selfie capture, stated DOB (18+), ToS attestation, display name. Profiles are minimalist — no event history shown.
- v0 liveness uses Apple-native primitives (AVFoundation + Vision framework) with a blink or head-turn challenge during selfie capture. This is a *light* liveness check — it deters casual bot signups but is defeatable by a motivated attacker holding up a photo or video. Stronger liveness (third-party passive SDKs such as Persona or AWS Rekognition Liveness) is a planned post-v0 upgrade once the wedge is validated and signup volume justifies the per-check cost.

**Map and discovery**
- Default surface on app launch is a 2D-tilted map of the user's region (LA in v0).
- A user-controlled time slider filters visible Events from "Live now" to "next 72hr", defaulting to 72hr.
- Pins encode category (icon/color) and temporal urgency (Live > soon > later > muted) within the selected window.
- Friend Attendees appear on pins for the friend; strangers see counts only.
- Exact pin location is fuzzed to neighborhood level until the user Commits, with a creator opt-out for explicitly public events.

**Event creation**
- Two shapes: Hosted (α) and Seeded (β). Categories are required from a fixed taxonomy (~10 categories).
- α-Events have an optional Cap. β-Events have a required Tip threshold (`>= 2`, no upper bound) and an optional Cap (Cap ≥ Tip threshold; otherwise unbounded). Unrealistic thresholds self-correct via failed Tip.
- Both shapes accept `start_time` in the range `[creation_time, creation_time + 72hr]`. Past start times are rejected. α-Events with `start_time = creation_time` are explicitly allowed (immediate-Live; e.g., a Host already on-site at a pickup game seeds an Event to attract drop-ins). For these, no "starts soon" reminder fires; the check-in affordance is available immediately upon Commit.
- Gating rules at creation: rep ≥ X, friends-only. Rules are evaluated against prospective Attendees at Commit time.
- β-Events default to a Tip deadline of `start_time - 1hr`. The Seeder may adjust this at creation within bounds: no later than `start_time - 15min`, no earlier than `creation_time` (i.e., the deadline must lie between event creation and 15 minutes before start). If the Event has not Tipped by the deadline, it Cancels and notifies the Seeder + any Committed Attendees.
- A Filling β-Event also auto-Cancels if its Commit count drops to 0 before the deadline (e.g., the Seeder and any others all Withdraw). This is a mechanical cleanup — there's no collective left to honor — and is **not** a back-door Seeder-cancellation, since it requires the Seeder *and* all others to have Withdrawn independently.

**Commitment**
- Commits are reversible at any time before and after Tip. Withdrawal is one-tap.
- Withdrawal classification: Withdraw (early) = clean; Flake (late) = reputation cost; Ghost (no Withdraw, no Show) = highest reputation cost.
- Tip is sticky on β-Events: Withdrawals do not un-Tip a Tipped Event, even if the count drops below threshold.
- Vacated slots reopen up to Cap and may notify nearby users.
- A Tipped β-Event whose count has dropped below its original Tip threshold is surfaced as **Thinning** — in-app promotional UX (pin treatment, time-slider promotion) to encourage new Commits to restore the room to the Seeder's stated threshold. No push notifications fire for Thinning in v0 (fatigue defense).

**At-event experience**
- Presence is resolved by **objective signals only** — check-in tap or passive location confirmation. No peer attestation in the Show/Ghost decision (per ADR 0009).
- **Check-in tap:** geofence-gated at ~50m around the Event pin, anti-fraud, single GPS query at tap time.
- **Passive location confirmation:** for users who granted iOS "Always Allow" location, any GPS sample within ~150m of the pin during the event window (15 min before scheduled start through scheduled end) confirms Show. Bounded in time, space, scope, and consent. Stops as soon as the event ends or the user leaves the polygon.
- If neither path produces presence and the Attendee didn't Withdraw, the outcome is Ghost. No third "Soft Ghost" disposition.
- Event chats unlock at creation (α) or at Tip (β). Chats remain writable for 24hr after Done, then archive read-only.

**Post-event**
- Within ~30 minutes of Done, Attendees are prompted with the post-event flow: 👍 / 👎 / skip per fellow Attendee, with a "what happened?" sheet (one or more hard reasons; or "I just didn't like them") on 👎.
- Of the captured signals, only **👎-with-hard-reason** moves the recipient's score (via `flag_factor`, see ADR 0008). 👍s and soft 👎s ("I just didn't like them") are captured for the recipient's bundled-feedback display but do not move the score. Score = trustworthiness, not popularity.
- Following the feedback step, users see fellow Attendees as Friend-request candidates.
- Show / Ghost disposition is determined entirely by objective signals (check-in + passive location), per ADR 0009.

**Reputation**
- Two scores: Attendee rep, Host rep. Both shown numerically (0–100) to the rated user and to other users at decision moments.
- Inputs to the score are **two**: behavioral and flag count. Other captured signals (👍s, soft 👎s) appear in the user's bundled feedback display but do not move the score.
  - **Behavioral** (Show / Withdraw / Flake / Ghost) — drives the score, severity-weighted (Ghost 3×, Flake 1.5×), Bayesian-smoothed (Beta prior α=4, β=1) for cold-start sanity, time-decayed (exponential, 2-year half-life).
  - **Flags** (👎 with at least one hard reason: "would not attend with again," "concerning behavior," "made me uncomfortable") — multiplicative penalty `flag_factor = max(0.4, 1 − 0.12 × weighted_flags)`. Floor at 0.4 bounds coordinated-harassment risk. A flag is invalidated if location data demonstrably places the flagged user elsewhere during the event window.
- Unrated is a distinct *display state* (a "New" badge until 3 outcome-recorded events accumulate; outcome = Show / Ghost / Flake — Withdraw doesn't count); the underlying numeric score is continuous via Bayesian prior, not a binary cutover. Tracked separately for Attendee and Host: a user can be a graduated Attendee but still "New Host" until 3 hosted events.
- The "New" badge is shown **alongside** the score, not as a replacement — score conveys the current Bayesian estimate, badge conveys "thin data, treat with grace."
- Flags are anonymous to the rated user. (Flagger-credibility weighting is deferred to v1.)

**Rep visibility — where the score appears:**

| Surface | Rep number visible? |
|---|---|
| Own profile (self-view) | Yes — Attendee + Host rep, plus event counts and periodic bundled-feedback summary |
| Another user's profile (drill-down) | Yes — Attendee + Host rep |
| α-Event detail card (Host) | Yes — both Attendee rep and Host rep |
| β-Event detail card (Seeder) | Attendee rep only — no Host accountability post-Tip |
| Attendee list on an Event | **No** — avatars + names + verification badges only. Drill-down to profile still surfaces rep. |
| In-event chat | No |
| Pin friend-visibility | No |
| Friend request received | Yes |
| Bring-a-friend invitation received | Yes (inviter + event creator) |
| Post-event feedback flow | No (would bias ratings) |

The rule: rep number appears at **decision moments** and on **deliberate profile inspection**, not on passive list/chat/pin surfaces. This avoids social-caste-ranking dynamics on lists while preserving signal where it's load-bearing for choice. Aggregate room signal — when a Host wants prospective Attendees to know "this is a high-trust room" — is communicated via the event's gating rule display (e.g., "rep ≥ 70 required"), not by exposing individuals.

**Bundled feedback to self:** periodic anonymous aggregate ("in the last 2 weeks: 4 👍, 1 hard flag for 'concerning behavior'"). No per-event attribution, no flagger identity. Cadence tunable; defer specifics until live.

**Friends**
- Bidirectional, request → accept. v0 unlocks: friends-only gating eligibility, DMs, contextual visibility on Event pins.
- No commit-history browsing, no real-time location, no rep vouching in v0.

**Privacy**
- Default location-fuzzing for non-Attendees.

**Notifications**
- Three buckets: required (Committed-event critical), default-on (Committed-event milestones, friend signal), opt-in (general discovery, digests). See CONTEXT.md §Notifications for the per-tier list.
- Throttling: only **Friend-Attending-nearby** is throttled (1/day cap per user, batched-rollup when multiple candidates qualify). All other tiers fire as state transitions occur; user behavior is the natural rate limiter.
- Deduplication: achieved via **idempotent state-transition handlers** (each push fires from a handler gated by a single timestamp column on the event row, e.g., `tipped_at`, `cancelled_at`, `starts_soon_pushed_at`). No separate push-receipts table.

**Reports**
- Distinct from rating flags. Severity tiers (info / concerning / urgent). Urgent reports may auto-restrict pending review. Routed to a human moderation queue.

**Surfaces**
- Bottom navigation: Map, Your Events, Friends, Profile.

---

## Non-functional requirements

**Safety and trust**
- Hard floor: 18+, phone-verified, live-selfie-verified (light liveness in v0; see Account and identity).
- Stranger-meeting must feel safer than the alternative (texting an Instagram acquaintance to hang out). Specific levers: location fuzzing, geofence-gated check-in, anonymous flags, separate report path with severity tiers, friends-only gating option.
- The platform's promise to women considering attending stranger Events is "the count you see is real, the people listed are verified, the Host is accountable." v0's verification floor (phone + light selfie liveness) is honest but limited — the strength of this promise grows materially with the post-v0 upgrade to third-party liveness.

**Privacy**
- No browsable history of any user's events. Friend signal is contextual, not browseable.
- A user's attendance can be hidden from their friend graph at the Event level (attend-privately).
- Anonymous flags and reports protect users from retaliation.

**Performance and feel**
- Cold-start: in v0 the map should feel populated even when actual density is low. A pre-signup demo seeds the perception of activity. The 72hr default time horizon is chosen partly for this reason.
- Pin updates should feel live (real-time count changes drive the wedge); push notifications should reflect threshold milestones promptly.

**Accessibility**
- TBD. Minimum: support for OS-level dynamic type and screen readers. Specific commitments deferred to design phase.

---

## Technical posture (v0)

This section records v0 stack/implementation decisions that aren't surprising enough to warrant standalone ADRs but are load-bearing enough to record here. Treat as locked-but-revisable.

**Stack (per ADRs 0003–0007):**
- iOS-first native (Swift/SwiftUI). Android deferred. (ADR 0003)
- MapLibre Native + Protomaps Basemaps fork + R2-hosted `.pmtiles`. (ADR 0004)
- Supabase data plane (Postgres + PostGIS, Auth, Storage) + a Go HTTP server for business logic. iOS hits both directly. **No Supabase Realtime.** (ADR 0005)
- Polling for pin counts (5–10s while map foreground); SSE for chat receive (transient, per-screen); APNS for async notifications. **No WebSocket.** (ADR 0006)
- Apple-native blink/head-turn liveness for v0 (AVFoundation + Vision). Third-party passive liveness (Persona, AWS Rekognition Liveness) deferred until distribution. (ADR 0007)

**Business-logic locus:** Business rules live in the Go server. Postgres triggers are reserved for *mechanical* concerns only (e.g., `NOTIFY` emit, denormalized count maintenance). No business logic in PL/pgSQL.

**Reputation system:**
- Score formula is locked in ADR 0008. `explainers/reputation.html` is the visual companion.
- **Recompute cadence:** real-time on input mutation. A single Go function `RecomputeReputation(userID)` is fired from (a) the post-event/Done handler (fan-out to all attendees of the Event) and (b) the flag-submit handler (single user). Behavioral input is known instantly at Done; flag input is human-reaction-bound, and the system reacts as fast as flags arrive.
- **Storage:** a separate `reputation` table keyed by `user_id` (NOT a denormalized column on `users`). Minimum fields: `user_id` (PK, FK), `attendee_score`, `host_score` (nullable), `attendee_event_count`, `host_event_count`, `last_recomputed_at`. Source of truth stays in `attendance_outcomes` and `flags`; the reputation row is a denorm cache.

**Push notifications:**
- APNS direct via `sideshow/apns2` in Go. One device-token table. **No** OneSignal / SNS / FCM intermediary.

**Reports — v0 stopgap:**
- A `reports` table with severity tier (info / concerning / urgent). Review via Supabase Studio. No purpose-built moderation UI. Real moderation tooling is deferred until distribution.

**Observability — v0 stopgap:**
- Structured logs via host built-ins (Fly.io / Railway). A small set of metrics-rollup views for product health (Show-rate, Tip-rate, Flake-rate, etc.). DataDog / honeycomb / similar deferred.

**Event lifecycle state (computed, not stored):**
- Lifecycle state for α and β Events is **derived at read time** from the row's timestamps (`start_time`, `end_time`, `tipped_at`, `cancelled_at`) and the Commit count vs. Tip threshold. There is no `state` column on `events`.
- A SQL function (or view) `event_state(events_row) returns text` computes the state — `'Open'` / `'Filling'` / `'Tipped'` / `'Live'` / `'Done'` / `'Cancelled'`. Application reads it as a normal column projection.
- Genuine state-transition writes only happen for events that need to record *that the transition occurred*: `tipped_at = now()` when count reaches threshold, `cancelled_at = now()` on Cancel. The `Open → Live` and `Live → Done` transitions are pure time-passage and require no write.
- For an α-Event created with `start_time = creation_time` (immediate-Live / on-site Host case), no special-case logic is needed: the row is inserted, `event_state()` returns `'Live'` immediately, and the check-in affordance is available right away. The Open phase has zero duration; the state machine handles it without intermediate Open writes.
- The Go server runs a periodic poll (e.g., every 30s) for events that have just transitioned to `'Done'` to fire post-event hooks (reputation recompute on attendees, post-event feedback prompts, etc.). Polling is sufficient at v0 scale; no DB-trigger-based fire-on-time-passage required.

**Friend graph (per ADR 0010):**
- `friendships` is a mirrored two-row table (`PRIMARY KEY (user_id, friend_id)`); one row per direction. Both rows written in the same transaction by the Go server on accept; both deleted on unfriend.
- `friendship_requests` is a separate single-row table (`PRIMARY KEY (requester, recipient)`); accept moves the relationship from `friendship_requests` into the mirrored `friendships`, reject deletes from `friendship_requests`.
- No `status` column on `friendships`. The presence of the row is the relationship.
- Choice of mirrored over canonical-pair is for RLS correctness surface (see ADR 0010), not raw performance — at v0 scale the performance delta is negligible.

**Geo data model on `events`:**
- `events.geom` — `geometry(Point, 4326)` — source of truth.
- `events.geog` — `geography(Point, 4326)` GENERATED column — for proximity math.
- `events.display_geom` — `geometry(Point, 4326)` — random-offset noised point set **once** at creation, never recomputed (otherwise repeated reads let an observer triangulate the true location). Public/non-Attendee-facing pin position when `location_visibility = 'fuzzed'`. (See CONTEXT.md §Location fuzzing.)
- `events.fuzz_radius_m` — `integer` (e.g., 200).
- `events.location_visibility` — `text` (`'fuzzed'` | `'public'`).
- Reverse-geocoded `display_label` (e.g., "Pickleball in Venice") deferred — fuzzed pin alone is sufficient for v0.
- Exact location reveals to a user only after they Commit (per CONTEXT.md §Location fuzzing); not at start time.

---

## Out of scope (v0)

The following are deliberately *not* in v0. Each was considered and pushed to v1+ with reason. (See `CONTEXT.md` and conversation history for context.)

- Official, paid, or ticketed events (concerts, venue events, promoter listings). Eventbrite-shaped products serve those.
- AR mode (camera-overlaid pins).
- Full 3D map rendering. Selective landmark extrusion is a v1 candidate.
- Photorealistic 3D flyover cinematics (e.g., "fly to event" via Google's Maps 3D SDK for iOS — https://mapsplatform.google.com/resources/blog/introducing-3d-maps-on-mobile-build-immersive-experiences-for-android-and-ios/). v2+ candidate; depends on paid/featured events landing first and the SDK leaving Experimental.
- Manual per-Commit Host approval (only rule-based auto-gating in v0).
- Gender-based gating rules and women-only events (require gender verification).
- Atomic-pair "I'll only Commit if my friend Commits" mode.
- Free-form tags on Events. Fixed taxonomy only.
- Real-time friend location ("Find My Friends" style).
- Friend rep vouching (your friend's good rep boosts yours).
- Flagger-credibility weighting (v0 treats all flags equally).
- 1–5 numeric ratings. Replaced in v0 by the 👍 / 👎 / skip ternary; see ADR 0008.
- **Attend privately.** Toggle at Commit time that would hide a user from their *own* friend graph in the context of a single Event (per-event random alias to friends; real identity to strangers). Use case: attending a singles meetup or similar without immediate social network noticing. Deferred for two reasons: (1) the projection is nontrivial — same row must render differently to friend vs stranger viewers, requiring a Postgres view layer or Go-only Attendee-detail endpoint with careful auth-aware projection logic; (2) the use case is niche at v0 scale and plausibly unmissed. Vocabulary and design intent preserved in CONTEXT.md for the v1 revisit.
- **Bring-a-friend (slot-hold invite mechanic).** A mechanic letting an Attendee invite a mutual Friend to an Event they've Committed to, with a slot held for ~10 minutes during which the invited Friend has first-priority claim. Brought Friends would still independently pass all Gating rules. Deferred for v0 because: (1) the underlying coordination is achievable via DMs (in v0) — friends can simply message each other a link and Commit independently; (2) the slot-hold mechanic introduces real concurrency complexity (transactional Cap accounting against active holds, race against stranger Commits, hold-expiry cleanup, public-count semantics) that earns its place only when discovery + cap pressure make the friend race a real problem; (3) the alternatives considered for the public-count behavior (hold consumes slot publicly vs. hold reserved invisibly with race-aware Commit endpoint) carry distinct UX trade-offs that benefit from real usage data before locking in. Vocabulary preserved in CONTEXT.md for the v1 revisit.
- **User-to-user blocking.** v0 has no block feature. Anti-creep coverage at v0 scale is provided by: hard-reason Flags reducing reputation (ADR 0008), Reports for severe incidents (severity-tiered, manual moderation via Supabase Studio per §Technical posture), Withdraw at any time, and pre-Commit Attendee-list inspection for self-avoidance. Block was considered in two scopes for v1+:
  - *Minimal scope* — a `blocks` table with RLS hiding blocker/blocked from each other's friend-suggestion and DM surfaces. Does not prevent co-Commits at the same Event.
  - *Full scope* — minimal-scope plus: blocked cannot Commit to blocker's events, blocker's events not shown on blocked's map, cleanup logic for existing co-Commits, ban-evasion-by-new-account considerations.
  Reason for deferral: block is a preference-driven user-control feature, not a load-bearing safety primitive — Reports cover the safety cases. Half-blocking (minimal scope) is worse than no blocking for user trust ("I blocked them, why are they still on the map at events?"), so v0 ships without it rather than with a partial implementation. Revisit at v1 with full scope when distribution and demand justify the engineering and RLS complexity.
- ID upload as a verification tier.
- Third-party passive-liveness SDKs (Persona, AWS Rekognition Liveness, FaceTec). v0 uses Apple-native blink/head-turn liveness; upgrading to a third-party passive SDK is a planned post-v0 step once signup volume justifies the per-check cost (~$0.10–$1.00) and the wedge is validated.
- Multi-city expansion. v0 is LA-only.
- Capability-gating thresholds beyond the New / Trusted / Restricted distinction, and reputation score-computation specifics. Both deferred until usage data exists.
- Web platform. Assumed native; final commitment in the technical design pass.

---

## Open questions

These are unresolved at PRD stage. Each will be addressed in the technical design pass or in usage tuning.

1. **The 10 categories themselves** — exact list, icons, colors. Content design.
2. **Reputation score computation** — input weights, score range, recompute cadence.
3. **Capability gating thresholds** — what reputation level enables hosting, what triggers Restricted state.
4. **Cold-start launch strategy** — single LA neighborhood vs. citywide, seeded "anchor" Events run by the team or partners.
5. **Web vs. native platform** — native is implied by the location/notification/camera-heavy posture, but warrants explicit decision.
6. **Specific 10-tooltip tutorial copy + pre-signup demo content.**
7. **Geofence radius for check-in** — ~50m is a starting point; calibration needed for indoor venues.
8. **Whether check-in unlocks any Live-only chat affordances** (e.g., live-emoji "I'm by the entrance"). Discussed informally; not committed.
9. **Literal notification copy** for Tip, Cancel, starts-soon, slot-opened, and similar push/in-app strings (content-design work; structural commitments are locked in §Functional Requirements and CONTEXT.md).
10. **Exact prompt copy for the in-app value-prop screens** preceding each iOS system prompt (location upgrade, notifications). The structural commitments are locked in CONTEXT.md §Permission posture; only the literal wording remains as content-design work.
11. **Failure modes for the 24hr post-event window** — chat archives mid-conversation, photo-upload failure, late-arriving feedback after archival, etc.

### Parked recommendations (not yet locked)

- **Q4 — Cold-start launch strategy:** hand-curated SQL seed of plausible Events around LA neighborhoods at launch; no synthetic generator. Locks the structural commitment that demo content is real curated data, not procedurally generated.

### Known v0 gaps (load-bearing risks)

- **Crashers.** People who didn't Commit but show up at an Event have no in-app handle and therefore cannot be reported through the flag flow (they're not in the Attendee list). Real safety gap; not addressed in v0. Possible v1 fixes: report-by-photo, Host-initiated stranger-add-and-flag, geofence-detected uncommitted-presence.

---

## Success criteria for v0

The v0 will be considered a successful launch if:

1. **Strangers Commit and Show.** A meaningful share of β-Events Tip and run with the Attendees who Committed. Show-rate is the primary product-health metric.
2. **The threshold mechanic feels exciting, not scary.** Users open the app idly and find watching the count tick up rewarding.
3. **No safety incidents that the report flow doesn't catch and resolve.** Zero is the target; "we caught it and acted" is the realistic floor.
4. **Friend graphs grow.** Stranger → Friend conversion (post-event Friend-add) is healthy.
5. **The map feels populated.** Even with low density, the cold-start mechanisms (72hr default window, fuzzed pins, demo content) keep the city looking active.

A failure mode to watch: the app feels like Meetup in different paint. If users only use α-Events and ignore β-Events, the wedge has not landed.

---

## References

- `CONTEXT.md` — canonical vocabulary, structural commitments, lifecycle states, and decision context.
- `docs/adr/0001-seeders-cannot-cancel.md` — Seeders cannot cancel β-Events.
- `docs/adr/0002-geofence-checkin-no-peer-attestation.md` — Attendance via geofence-gated check-in, not peer attestation. (**Superseded by ADR 0009** — disposition matrix had a flaw.)
- `docs/adr/0003-ios-first-native.md` — iOS-first native (Swift/SwiftUI); Android deferred.
- `docs/adr/0004-maplibre-native-with-self-hosted-protomaps.md` — MapLibre Native + Protomaps Basemaps fork + R2-hosted `.pmtiles`.
- `docs/adr/0005-supabase-data-plane-go-server-business-logic.md` — Supabase data plane (Postgres + PostGIS, Auth, Storage) + Go HTTP server for business logic. iOS hits both directly. No Supabase Realtime.
- `docs/adr/0006-poll-pins-sse-chat-push-async.md` — Polling for pin counts (5–10s while map foreground); SSE for chat receive (transient per-screen); APNS for async. No WebSocket.
- `docs/adr/0007-apple-native-liveness-v0.md` — Apple-native blink/head-turn liveness for v0 (AVFoundation + Vision); third-party passive liveness deferred.
- `docs/adr/0008-three-tier-reputation-weighting.md` — Reputation has two scoring inputs (behavioral + flag_factor), multiplicative penalty asymmetry, 0–100 scale, 2-year half-life decay.
- `docs/adr/0009-presence-objective-signals-only.md` — Presence (Show vs Ghost) by objective signals only (check-in tap or passive location). Soft-Ghost removed; flags don't determine presence.
- `docs/adr/0010-friendships-mirrored-rows.md` — Friendships stored as mirrored two-row pairs (not canonical-pair) for RLS correctness surface. Pending requests in a separate table.
- `explainers/reputation.html` — visual companion to the reputation formula in ADR 0008.
