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
26. As an Attendee, I want to be able to invite a mutual Friend to an Event I've Committed to, with a slot held for them for ~10 minutes, so that we can coordinate without one of us losing the seat.
27. As an Attendee, I want my Friend's invitation to still be subject to the Event's gating rules, so that bring-a-friend is not a vouching loophole.

### Privacy and presence

28. As an Attendee, I want my exact identity hidden from Friends I haven't told I'm attending an Event ("attend privately"), so that I can go to a singles meetup or similar without my immediate social network noticing.
29. As an Attendee, I want strangers at the Event to still see my real identity even when I'm attending privately, so that the social experience isn't broken — privacy is from my friend graph, not from the people I came to meet.
30. As a user, I want never to be able to browse another user's upcoming events or commit history, so that the platform isn't a stalking surface.

### At the Event

31. As an Attendee, I want to check in at the Event when I arrive by tapping a button, so that my Show is recorded.
32. As an Attendee, I want check-in to be gated by my GPS being near the Event pin, so that no one can defraud the system by checking in remotely.
33. As an Attendee on an Event that has Tipped (β) or been created (α), I want a group chat with the other Attendees, so that we can coordinate logistics ("I'm by the picnic table, red shirt").
34. As an Attendee, I want the chat to remain accessible for 24 hours after the Event is Done, so that we can share photos and make plans for next time, before it archives.

### After the Event

35. As an Attendee, I want a prompt within ~30 minutes of the Event ending to rate my experience, so that the system gets honest feedback while it's fresh.
36. As an Attendee, I want a prompt to add the people I just met as Friends after the Event, so that the network effect of stranger-to-friend conversion has the highest-yield surface.
37. As an Attendee, I want the option to flag a fellow Attendee as "would not attend with again" anonymously, so that I can warn the system without confronting them.

### Reputation and trust

38. As a user evaluating whether to Commit to an Event, I want to see the Host's Host rep (for α) or be reassured by the Tip threshold mechanic (for β), so that I have signal on the trustworthiness of the gathering.
39. As a Host, I want to see prospective Attendees' Attendee rep so I can decide whether to set a rep gate, so that I can shape the room's reliability.
40. As a new user with no rep yet, I want to be visibly *Unrated* rather than low-rep, so that I'm not punished for being new.
41. As any user, I want my flags to be anonymous to the people I'm flagging, so that I can speak honestly without fear of retaliation.
42. As a user who experiences a serious safety incident (harassment, threats, assault), I want a clearly distinct **Report** path that goes to a human moderation queue with severity tiers, so that the system treats it as more than a rating signal.

### Friends

43. As a user, I want to send and accept Friend requests bidirectionally, so that the friend graph is opt-in on both sides.
44. As a user, I want to DM Friends outside any specific Event chat, so that we can coordinate plans privately.
45. As a user, I want to be eligible for "friends only" gated Events created by my Friends, so that intimate gatherings are possible on the platform.

### Notifications

46. As a user, I want unmissable notifications for Events I've Committed to (starts soon, slot opened, cancellation), so that I never miss a real commitment.
47. As a user, I want notifications about milestones on my Committed Events (Tipped, slot opened) by default, so that I feel the momentum.
48. As a user, I want to opt *into* discovery notifications rather than have them on by default, so that I'm not pushed into churn by overzealous surfacing.

### Surfaces

49. As a user, I want a "Your Events" tab that lists my upcoming and recent Commits, so that I can see at a glance what I'm doing.
50. As a user, I want tapping an Event in the list to pan the Map to its pin and open its detail, so that the Map remains the canonical view rather than a parallel one.

---

## Functional requirements

The following capabilities define the platform. Each is grounded in the design conversation captured in `CONTEXT.md`.

**Account and identity**
- Sign-up requires phone verification, live in-app selfie capture, stated DOB (18+), ToS attestation, display name. Profiles are minimalist — no event history shown.

**Map and discovery**
- Default surface on app launch is a 2D-tilted map of the user's region (LA in v0).
- A user-controlled time slider filters visible Events from "Live now" to "next 72hr", defaulting to 72hr.
- Pins encode category (icon/color) and temporal urgency (Live > soon > later > muted) within the selected window.
- Friend Attendees appear on pins for the friend; strangers see counts only.
- Exact pin location is fuzzed to neighborhood level until the user Commits, with a creator opt-out for explicitly public events.

**Event creation**
- Two shapes: Hosted (α) and Seeded (β). Categories are required from a fixed taxonomy (~10 categories).
- α-Events have an optional Cap. β-Events have a required Tip threshold and an optional Cap (Cap ≥ Tip threshold).
- Gating rules at creation: rep ≥ X, friends-only. Rules are evaluated against prospective Attendees at Commit time.
- β-Events default to a Tip deadline of `start_time - 1hr`; if not Tipped by then, the Event Cancels and notifies Committed Attendees.

**Commitment**
- Commits are reversible at any time before and after Tip. Withdrawal is one-tap.
- Withdrawal classification: Withdraw (early) = clean; Flake (late) = reputation cost; Ghost (no Withdraw, no Show) = highest reputation cost.
- Tip is sticky on β-Events: Withdrawals do not un-Tip a Tipped Event, even if the count drops below threshold.
- Vacated slots reopen up to Cap and may notify nearby users.
- Bring-a-friend invites a mutual Friend with a 10-minute slot-hold; the invitee must independently pass all Gating rules.

**At-event experience**
- Check-in is one-tap, gated by user GPS being within ~50m of the Event pin at tap time. No continuous geofencing in v0.
- Event chats unlock at creation (α) or at Tip (β). Chats remain writable for 24hr after Done, then archive read-only.

**Post-event**
- Within ~30 minutes of Done, Attendees are prompted to rate. α: bidirectional Host ↔ Attendee rating. β: event rating + optional anonymous outlier flags on specific Attendees.
- Following the rating, users see fellow Attendees as Friend-request candidates.
- Show / Ghost / Soft-Ghost disposition is computed from check-ins + peer flags per the matrix in `CONTEXT.md`.

**Reputation**
- Two scores: Attendee rep, Host rep. Scores are private to the rated user (visible numerically) and visible contextually to other users at decision moments.
- Inputs: behavioral signal (Show/Withdraw/Flake/Ghost ratios) weighted heavily; subjective ratings + flags weighted lower as outlier signal.
- Unrated is a distinct state from low-rep. New users are not punished.
- Ratings/flags are anonymous to the rated user. (Flagger-credibility weighting is deferred to v1.)

**Friends**
- Bidirectional, request → accept. v0 unlocks: friends-only gating eligibility, DMs, bring-a-friend, contextual visibility on Event pins.
- No commit-history browsing, no real-time location, no rep vouching in v0.

**Privacy**
- Default location-fuzzing for non-Attendees.
- Attend-privately mode hides a user from their own friend graph in the context of a single Event (per-event random alias to friends; real identity to strangers).

**Notifications**
- Three buckets: required (Committed-event critical), default-on (Committed-event milestones, friend signal), opt-in (general discovery, digests).

**Reports**
- Distinct from rating flags. Severity tiers (info / concerning / urgent). Urgent reports may auto-restrict pending review. Routed to a human moderation queue.

**Surfaces**
- Bottom navigation: Map, Your Events, Friends, Profile.

---

## Non-functional requirements

**Safety and trust**
- Hard floor: 18+, phone-verified, live-selfie-verified.
- Stranger-meeting must feel safer than the alternative (texting an Instagram acquaintance to hang out). Specific levers: location fuzzing, geofence-gated check-in, anonymous flags, separate report path with severity tiers, friends-only gating option.
- The platform's promise to women considering attending stranger Events is "the count you see is real, the people listed are verified, the Host is accountable."

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
- ID upload as a verification tier.
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
7. **Cap on β-Event Tip-threshold range** — stated as 3–20 informally; needs a hard answer.
8. **Geofence radius for check-in** — ~50m is a starting point; calibration needed for indoor venues.
9. **Whether check-in unlocks any Live-only chat affordances** (e.g., live-emoji "I'm by the entrance"). Discussed informally; not committed.

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
- `docs/adr/0002-geofence-checkin-no-peer-attestation.md` — Attendance is resolved by geofence-gated check-in, not peer attestation.
