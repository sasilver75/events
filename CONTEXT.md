# Spontaneous Events

A spontaneous, location-based social events app for short-horizon (≤72hr) gatherings between strangers, with an optional friend graph. Events come in two shapes: Host-led and threshold-confirmed. Inspired by the playful, map-driven discovery of Pokémon Go and the interest-based gathering of Meetup, but distinct from both — the wedge is "spontaneous-and-local-with-strangers."

**Out of scope for v0:** Official / paid / ticketed events (concerts, venue events, promoter listings). Eventbrite-shaped products serve those well; v0 focuses on user-created informal events to protect the wedge and avoid the payments primitive. AR mode (camera-overlaid pins) is also explicitly out of scope.

## Map style

The map is **2D with a tilted perspective** (think Google Maps' 3D mode angle, but rendered against a 2D tile surface), with a custom theme distinct from off-the-shelf cartography. Pins use rich, animated, Pokémon-Go-style treatment to carry the playful vibe. **Selective 3D extrusion** of major landmarks (large buildings, hills, beaches) is a v1 candidate, gated on whether it earns its weight in product value. Full 3D rendering and AR are deferred.

The map is the **default surface** of the app on launch — feed/list views are secondary. A user-controlled time slider filters which Events are shown, ranging from "Live now" to "Next 72hr." Default is 72hr (full visible window) — cold-start social apps suffer from feeling empty, and showing more by default makes the city feel populated. Visual treatment encodes temporal proximity within the selected window: Live > starting soon > later > muted-and-far.

## Language

### Event types

**Event**:
A real-world gathering coordinated through the app, scheduled ≤72hr from creation. Always either a Hosted Event (α) or a Seeded Event (β) — never both.
_Avoid_: meetup, gathering, hangout.

**Hosted Event** (α):
An Event with a single accountable Host who proposes, runs, and is the face of the event. Familiar Meetup/Eventbrite shape.
_Avoid_: organized event, host-run event.

**Seeded Event** (β):
An Event that does not exist as a binding plan until N strangers Commit, at which point it Tips. The Seeder proposes but holds no special accountability post-Tip.
_Avoid_: threshold event (the threshold is the *mechanic*; β is the *shape*).

### People

**Host**:
The accountable initiator of an α-Event. Carries elevated verification + reputation requirements. A Host is also an Attendee of their own Event.
_Avoid_: organizer, owner, creator.

**Seeder**:
The initiator of a β-Event. An equal participant once the Event Tips; not accountable in the Host sense. Also an Attendee. Notably, a Seeder **cannot cancel** the β-Event they created — they can only withdraw their own Commit. β-Events belong to the collective of Committed Attendees, not to the Seeder.
_Avoid_: starter, creator.

**Attendee**:
A user who has Committed to an Event.
_Avoid_: participant, RSVP'er.

### Actions and states

**Commit**:
A user's binding RSVP. On α-Events, an attendance pledge with reputation stake. On β-Events, also counts toward the Tip threshold. Stronger than "RSVP" — a Commit carries reputation risk if dishonored.
_Avoid_: RSVP, signup, join.

**Withdraw**:
The verb. A user releases a previously placed Commit. Always available; always one-tap; never punitive in copy. Withdrawing is *preferred* over Ghosting.
_Avoid_: cancel (overloaded with Event cancellation), unjoin, leave.

**Flake**:
A categorization applied to a Withdraw that happens within a "late" window (post-Tip on β-Events; <2hr from Live on either shape). Not a distinct user action — a label the system attaches to a costly-but-honest Withdraw. Reputation-affecting.
_Avoid_: bail, late cancel.

**Show**:
A detected post-event state: an Attendee was confirmed present at the Event during the Live window. Confirmed by *either* a successful check-in tap (geofence-gated, ~50m of the pin) *or* passive location confirmation — a single GPS sample inside the Event's wider passive-presence radius (~150m) at any point during the event window (15 minutes before scheduled start through scheduled end). Passive confirmation requires the user to have granted iOS "Always Allow" location permission; otherwise the check-in tap is the only path to Show.
_Avoid_: present, attended, arrived.

**Ghost**:
A detected post-event state: an Attendee neither Withdrew nor was confirmed Show. The most reputation-costly behavioral outcome in the system; strictly worse than a Flake. Determination is **objective only** — no peer attestation enters the Show/Ghost decision.
_Avoid_: no-show, miss.

**Tip threshold**:
The minimum Commits required to Tip a β-Event. Set by the Seeder at creation. The only constraint is `tip_threshold >= 2` — the Seeder counts as one Commit (they are an Attendee of their own Event), so the minimum threshold reflects "Seeder + at least one stranger." No upper bound is enforced; unrealistic thresholds self-correct via failed Tip.

**Cap**:
The maximum Commits allowed on an Event. Optional; may be unbounded. Applies to both α and β. For β-Events, Cap ≥ Tip threshold.

### Reputation

**Attendee rep**:
A score every user has, reflecting their reliability and conduct as an Attendee. Updated post-Event by the Host (α) or by peer attestation (β). Single numeric value, displayed contextually.

**Host rep**:
A separate score Hosts accrue, reflecting the quality of α-Events they run. Updated post-Event by Attendees. Only Hosts have a Host rep; non-Hosts have N/A.

**Rating**:
A subjective numeric post-Event input to reputation. On α-Events: bidirectional (Host rates each Attendee; each Attendee rates the Host). On β-Events: each Attendee gives one overall event rating. Ratings are noisy by nature ("I had an okay time" → 4 stars instead of 5 carries little intentional weight) and are therefore **lightly weighted** — a bounded modifier (±5 points) on the behavioral score. Their role is to soften or sharpen the behavioral picture, not to drive it.

**Flag**:
A subjective post-Event input distinct from a Rating. An Attendee can flag specific other Attendees with semantically loaded labels — "would not attend with again," "concerning behavior." Unlike a Rating, a Flag is a strong intentional signal: the flagger took deliberate action to mark someone. Flags therefore carry **significantly more weight** than rating points (multiplicative penalty on the score, with a floor to bound coordinated-harassment risk). The asymmetry is deliberate: a reliable-show user who accumulates flags should not score well, because reliability without trustworthiness is not what the system rewards.

A Flag does **not** determine presence. Show vs Ghost is resolved by objective signals (check-in or passive location); a Flag is an orthogonal signal about behavior at the event. A Flag is **invalidated** if objective location data demonstrably places the flagged user elsewhere during the event window — they couldn't have been there to misbehave. If location data is unavailable for the flagged user (no permission, no GPS), the Flag is accepted: they may have been at the event with their phone off.

Both Ratings and Flags are **anonymous to the rated user** — they see their score change and possibly aggregate counts, but never who said what. *Deferred post-v0:* weighting flags by the flagger's own credibility (corroborated flags carry more weight; bad-faith flaggers degrade their own signal).

**Unrated**:
The state of a user who has no Attendee or Host rep yet (insufficient history). Treated distinctly from low-rep users — Unrated is a graduated-trust state for newcomers, not a punishment. Specific tier labels and gating thresholds are deferred until capability-gating is actually being designed.

### Privacy

**Location fuzzing**:
By default, an Event's exact location is hidden from non-Attendees. The map shows a neighborhood-level pin (e.g., "Venice"). Exact location reveals to a user only after they Commit. The Host/Seeder may opt out at creation for genuinely public events (e.g., "sunset at the pier"). This default protects safety (no scoping/stalking) and event integrity (no crashing).

Structural commitment: the **fuzzed pin position is set once at Event creation and never recomputed**. Repeated re-randomization across reads would let an observer triangulate the true location by averaging samples; a single set-once offset eliminates that attack.

**Gating**:
A rule attached to an Event at creation that restricts who can Commit. v0 supports **rule-based auto-gating only** (e.g., "rep ≥ X," "friends only"). Rules evaluate against the prospective Attendee at Commit time. Manual per-Commit approval by Hosts is deferred post-v0. Gender-based rules (e.g., "women only") are locked in concept but require gender verification, also deferred post-v0.

**Category**:
Each Event has exactly one Category from a small fixed taxonomy (~10 categories, e.g., Sports, Food/Drink, Music, Outdoors, Games, Social, Creative, Wellness, Networking, Other). Required at creation. Categories drive pin icon/color and map filtering. Structured sub-tags and freeform tags are deferred post-v0.

## App surfaces

The bottom navigation has four tabs:

1. **Map** — the default surface; the canonical view of Events.
2. **Your Events** — calendar/list of upcoming + recent Commits. Tapping an entry pans the Map to the pin and opens the event detail (chat, comments, photos). The list is an alternative entry into the same canonical Map view, not a parallel UI.
3. **Friends** — friend list + DMs.
4. **Profile** — own profile, settings, verifications.

## Onboarding

Account creation is **required before browsing the real map** — guest-browse is deferred. Friction is structured as **mixed** (verification upfront, personalization light afterward, permissions just-in-time):

| # | Screen | Job |
|---|---|---|
| 0 | Pre-signup demo | Animated map with fake-but-plausible Events around the user's approximate location; quick feature highlights; Skip-to-signup CTA always visible |
| 1 | Phone number | SMS verify |
| 2 | Live selfie capture | In-app camera, single shot. Used for verification *and* as the user's default avatar |
| 3 | DOB + ToS | Stated age (18+) + attestation |
| 4 | Display name (required) + optional bio | Identity flair |
| 5 | Land on map; JIT permissions | Location: "When in Use" requested at first map view (centering); upgrade to "Always Allow" requested at first Commit (passive presence). Notifications requested at first Commit. Skippable tutorial overlays (~3 tooltips) introduce key affordances. |

Specific demo content and tutorial copy are deferred — refinement needs more product sense than v0 has yet. The flow above is the structural commitment.

## Attendance and presence confirmation

Presence is resolved by **objective signals only** — the check-in tap and passive location confirmation. Peer attestation never enters the Show/Ghost decision (peers are not surveillance proxies; that's the principle in ADR 0009).

There are two paths to a confirmed Show:

1. **Check-in tap** — Attendees can tap a "I'm here" prompt at any time during the Live state (from `start_time` to `end_time`). The tap is **geofence-gated** — rejected unless the user's GPS places them within ~50m of the Event's pin. The tap is anti-fraud and cheap (one location query, no background polling).

   A push + in-app banner reminder fires once at Live time (i.e., at `start_time`) to nudge Attendees who haven't yet checked in. The reminder is a nudge, not a gate — the tap affordance is always available during Live with or without the reminder having fired. For α-Events with `start_time = creation_time` (immediate-Live), the reminder fires alongside Commit confirmations rather than as a scheduled push.

2. **Passive location confirmation** — for users who granted iOS "Always Allow" location permission, the app passively checks whether at least one GPS sample places them within the Event's wider passive-presence radius (~150m) at any point during the event window (15 minutes before scheduled start through scheduled end). Any single sample inside the polygon during the window is sufficient. If they enter then leave during the event, that still counts as Show — leaving early is acceptable in v0.

Either path produces a Show. If neither produces presence confirmation **and** the Attendee did not Withdraw, it's a Ghost. There is no "Soft Ghost" disposition — the previous distinction depended on peer attestation, which is no longer used to determine presence.

The wider passive radius (150m vs the tap's 50m) is intentional: the tap is anti-fraud (you could spoof presence by tapping if it were too loose), while passive GPS confirmation is harder to fake (background reading from iOS) so we lean toward catching real attendees through GPS noise rather than missing them.

**Permission posture.** Location permission is requested in **two escalating steps**, not one:
1. **First map view:** "When in Use" location, framed as "center the map on you, find events near you." Lets the map function during browsing without commitment.
2. **First Commit:** *upgrade* to "Always Allow," framed as "we'll confirm you arrived without you needing to tap, and stop tracking when the event ends."

If the user declines the upgrade at step 2, they fall back to check-in-tap-only (no passive presence confirmation). If the user declines step 1 entirely, the map falls back to a default LA-region view with manual pan (no user centering). No third path.

**First-Commit permission sequence.** The Commit transaction completes *before* permission asks, so the Commit never fails because of a denied permission. Order: (1) user taps Commit → (2) Commit succeeds and the user sees a celebratory confirmation → (3) in-app value-prop screen explains the location upgrade → (4) iOS system prompt for "Always Allow" → (5) in-app value-prop for notifications → (6) iOS system prompt for notifications → (7) return to event detail. The Commit's protection from a permission denial is the explicit principle: nothing about permissions can break a Commit.

**No re-prompts in v0.** iOS shows its system permission dialog exactly once per permission per app install; subsequent grants require a manual trip to Settings. v0 does **not** chase declined permissions with in-app re-prompts, banners, or contextual nags. The user's initial answer is honored. Re-prompt strategy is deferred until usage data shows decline-then-regret is a real pattern worth addressing.

## Post-event experience

Within ~30 minutes of an Event going Done, the user is prompted to rate their experience (Host rating on α; event rating + optional Attendee flags on β). After rating, they're shown fellow Attendees as cards with one-tap **Friend-request** — converting strangers into Friends is the network-effect engine of the product, and the post-event window is the highest-yield moment for that conversion. Optional photo upload to the read-only chat is allowed during the 24hr afterglow window. No share-to-social, streaks, or badges in v0.

Friend-adds are deliberately **post-event, not in-event** — pulling out a phone mid-event to friend someone breaks presence; the next-morning "I had fun, let me lock that in" moment is the right prompt.

## Notifications

Three buckets:

**Required (cannot disable):** Reminders and state changes for Events the user has Committed to (starts-soon, Live time, **Tip** on β-Events — the moment intent becomes a binding plan, Host-cancellation, removal). Account-critical alerts (rep changes affecting capabilities, flags, security).

**Default ON, opt-out:** Non-Tip threshold milestones on Committed Events (slot opened, count milestones). Direct messages from Friends. Friend-Attending-nearby (smart-throttled to avoid spam in dense friend graphs).

Note: **Thinning** state on a Committed β-Event does **not** trigger a push notification in v0. Thinning is surfaced in-app only (pin pulse, time-slider promotion). Push to encourage new Commits is deferred until engagement data justifies it.

**Default OFF, opt-in:** General discovery ("events Tipping near you tonight"), weekly digests, interest-matching beyond immediate vicinity.

Discovery is deliberately opt-in. The product risk is push-fatigue churn, not under-notification — get users addicted to *opening* the app for discovery rather than receiving discovery passively.

**Throttling and deduplication.** Only one notification type is throttled in v0: **Friend-Attending-nearby has a hard cap of 1 push per user per day**, with multiple eligible candidates batch-rolled into a single "N friends are at events near you tonight" push. Cap state is tracked per-user (e.g., a `last_friend_attending_push_at` timestamp). All other notifications fire as their underlying events occur — the cadence is naturally rate-limited by user behavior (people don't have 10 events Tipping per day). Push deduplication is achieved by **idempotent state-transition handlers**, not a global push-receipts table: Tip / Cancel / starts-soon / Live and similar pushes fire from handlers gated by a single timestamp column on the event row, so re-running the handler is a no-op and the push it would emit is automatically deduped for free.

**Event chat**:
A group chat scoped to an Event. α-Events have a chat from creation (they're real from creation). β-Events unlock chat only at Tip — pre-Tip Attendees are anonymous to each other. The Tip moment doubles as the "curtain rises" UX moment when the chat appears. Chat remains writable for 24hr after the Event is Done, then archives read-only.

**Bring-a-friend** *(deferred to v1 — see PRD-v0 §Out of scope)*:
A mechanic letting an Attendee invite a mutual Friend to an Event they've Committed to. The inviter Commits unconditionally (count increments). A slot is held for the invited Friend for ~10 minutes; during the hold, that Friend has first-priority claim on the slot but the public count reflects only real Commits. If the Friend accepts, they Commit normally. If they decline or time out, the slot releases and may notify nearby users. Brought Friends must independently pass all Gating rules — bring-a-friend is a UX nicety, not a vouching mechanism. Vocabulary preserved here for v1 continuity; in v0 friends coordinate via DMs and Commit independently.

**Friend**:
A mutual relationship between two users (bidirectional, request → accept). In v0, friendship unlocks: eligibility for "friends-only" gated events, direct messages outside Event chats, and contextual visibility of friend Attendees on Event pins (see Friend visibility). Snooping powers (commit history, real-time location, vouching toward reputation) are deliberately **not** granted in v0. Bring-a-friend invitations are deferred to v1 (see PRD-v0 §Out of scope).

**Friend visibility**:
On an Event pin, a user sees the avatars/names of *their own friends* who are Attending. Strangers' identities are never shown on pins (only the count). Friend signal is **contextual**, not browseable — a user discovers friends in the context of nearby events, but cannot pull up a friend's profile and audit upcoming events.

**Profile**:
A minimalist destination page for a user. Shows: avatar, display name, optional bio, optional cosmetic personalization (banner, accent color, pronouns — Discord-flavored flair, not information density), Attendee rep, Host rep (when applicable), verification badges, mutual-friends *count* (not list), and member-since date. **No event history** — past Commits and attendance are deliberately not shown to preserve the no-snooping rule. The application's center of gravity is the real-world events, not profile-browsing.

**Attend privately** *(deferred to v1 — see PRD-v0 §Out of scope)*:
A toggle at Commit time that hides the user from their **own friend graph only** in the context of this Event. Friends do not see the user's avatar on the pin, do not see them in the Attendee list, and see them as a per-event random alias in chat. Strangers see the user normally — Attend privately is *social-graph discretion*, not anonymity. Use case: attending an event a user doesn't want their immediate social network to see them at (e.g., a singles meetup), without preventing the user from genuinely connecting with the strangers they're meeting. Vocabulary preserved here for v1 continuity; no v0 implementation.

**Tip / Tipped** (β-only):
The moment a β-Event reaches its Tip threshold and becomes a binding plan. The Event remains Tipped through to Live. **Tip is sticky** — Withdrawals after Tip do not un-Tip the Event, even if the count drops below the Tip threshold. Vacated slots remain open up to the Cap, and new Commits can fill them.
_Avoid_: fire, lock, hatch, confirm.

**Thinning** (β-only, derived state):
A Tipped β-Event whose Commit count has dropped below its original Tip threshold (e.g., Tip=6, count fell to 4 after two Withdrawals). The Event remains Tipped and will go Live, but is given **in-app promotional treatment** — pin pulse/accent, optional surfacing in the time-slider window — to encourage new Commits to bring the room back up to the Seeder's stated threshold. Thinning is a derived boolean (`is_tipped AND count < tip_threshold`), not a lifecycle state. **No push notifications fire for Thinning in v0** (fatigue defense; the in-app signal is sufficient at launch scale).

**Live**:
The state during which an Event is actually happening — attendees are expected to be physically present. Applies to both α and β.
_Avoid_: in-progress, active, started.

### Lifecycle states

α-Event states: **Open → Live → Done** (or **Cancelled**)

β-Event states: **Filling → Tipped → Live → Done** (or **Cancelled**, including failure-to-Tip)

State is **computed**, not stored — it is fully derivable from a few timestamp columns (`start_time`, `end_time`, `tipped_at`, `cancelled_at`) and the count vs. threshold relationship. There is no `state` column to keep in sync. The Open phase of an α-Event may have zero duration when the Seeder/Host creates the event with `start_time = creation_time` (immediate-Live, e.g., a Host already on-site at a pickup game) — the state machine handles this naturally because `event_state(row)` simply returns `'Live'` once `now() >= start_time`. (See PRD-v0 §Technical posture for the computation.)

α-Event `start_time` must satisfy `creation_time ≤ start_time ≤ creation_time + 72hr`. Past start times are rejected at creation. β-Event `start_time` carries the same bounds, plus the implicit constraint that the Tip deadline must lie between `creation_time` and `start_time - 15min`.

## Relationships

- An **Event** is either a **Hosted Event** or a **Seeded Event** — never both.
- A **Hosted Event** has exactly one **Host**.
- A **Seeded Event** has exactly one **Seeder**.
- A **Host** is also an **Attendee** of their own Event.
- A **Seeder** is also an **Attendee** of their own Event.
- A **Seeded Event** has 0..1 **Tip** transitions in normal flow; un-Tip is possible if Attendees withdraw post-Tip and drop the count below threshold.
- A **Commit** belongs to exactly one **Attendee** and one **Event**.

## Example dialogue

> **Designer:** When a **Seeder** creates a β-**Event** for pickleball with threshold 6, what state is it in?
> **PM:** Filling. It stays Filling until 6 **Commits** land, then it Tips.
> **Designer:** And if it Tips at noon but the event isn't until 7pm?
> **PM:** It's Tipped from noon to 7pm. At 7pm it goes Live. If two **Attendees** Withdraw at 5pm and the count drops to 4, it stays Tipped — **Tip is sticky**. The slots stay open up to the Cap, and because the count fell below the original threshold the Event surfaces as **Thinning** — UX-promoted to nearby users to bring the room back up.
> **Designer:** Can the **Seeder** withdraw their **Commit**?
> **PM:** Yes — same rules as anyone else. They have no special status post-Tip.

## Flagged ambiguities

- "Host" was initially used loosely for both Event shapes — resolved: **Host** is α-only; β has a **Seeder**.
- "Fire" was initially used for the threshold-met moment but conflated with "event is happening now" — resolved: **Tipped** is the threshold-met state, **Live** is the in-progress state. They can be separated by hours.
- "Cancel" was initially used loosely for any event termination — resolved: only **Hosts** can cancel α-Events; β-Events terminate via failed-Tip (deadline) or platform action. Tip is sticky — Withdrawals do not un-Tip. **Seeders cannot cancel.**
- "Low rep" was initially conflated with "new user" — resolved: **Unrated** is a distinct state from low-rep. New users get graduated trust; users with earned bad rep face cuts. Specific tier labels deferred.
