# Monetization — ideation

**Status:** ideation, not committed. Captures a brainstorming session on
v0/post-v0 monetization. Decisions here are *recommendations* awaiting
explicit lock-in; nothing in this doc has been promoted to PRD/ADR yet.

**Pickup hook:** if returning to this, the open decisions are listed in
[Open decisions](#open-decisions) at the bottom.

---

## Framing

Spur's moat is the reputation/trust system (`PRD-v0.md:233`: *"the count
you see is real, the people listed are verified, the Host is
accountable."*). Any monetization that lets users **buy trust or buy
attention** corrodes the primitive being charged for. The hard rule:
**monetize logistics around Events, never the social graph or trust
signals themselves.**

Concretely:
- ✅ Payments rails, scheduling, history, analytics, calendar sync, recurrence
- ⚠️ Visibility/reach for paid Events (cordoned, clearly labeled)
- ❌ Boosted ranking, paid verification badges, paid "see who Committed,"
  ad-supported feed

---

## Direction 1 — Take rate on paid Events

**Status:** recommended for *schema-in-v0, launch-in-v0.5*.

### Mechanics

- **Stripe Connect Express.** Hosts onboard as Connected Accounts; Stripe
  handles KYC, 1099-K issuance, payouts. App controls onboarding UX,
  doesn't see card data. (Standard exposes a Stripe dashboard to Hosts but
  more onboarding friction; Custom dumps compliance burden on us.)
- **8% platform fee + Stripe processing pass-through.** On a $20 ticket:
  Stripe takes ~$0.88 (2.9% + $0.30), app takes $1.60, Host receives
  $17.52. Reference: Eventbrite ~3.7% + $1.79; Partiful free. Sub-5%
  leaves no margin to fund refunds/disputes/safety; >10% pushes Hosts to
  Venmo-on-the-side. Easier to lower later than raise.
- **Platform-default refund policy, no Host customization in v0.** Full
  refund on Withdraw any time before check-in window opens; no refund
  after. Reason: `PRD-v0.md:232` makes Withdraw-at-any-time a load-bearing
  safety guarantee — letting Hosts set "no refunds ever" silently turns
  Withdraw into a punitive choice on paid Events.
- **Host cancellation forks [ADR 0001](../adr/0001-seeders-cannot-cancel.md).**
  Seeders can't cancel; paid Hosts *must* be able to (legally must issue
  refunds). Cancellation = full refund to all Committed Attendees,
  platform fee returned, ding to Host rep. Worth a follow-up ADR before
  launch.
- **No-show semantics.** Attendee no-show = payment kept by Host (minus
  app fee), separate from rep ding. Host no-show = full refund + larger
  rep ding. The monetary lever is *additive* to rep, not a substitute.

### Data model deltas (forward-compatible — schema can land in v0)

```
users.stripe_account_id text
events.is_paid bool, events.price_cents int, events.currency text (default 'USD')
commits.payment_intent_id text
commits.amount_cents int, commits.platform_fee_cents int
commits.payment_status text -- pending|paid|refunded|failed|disputed
```

### Out of scope for the first cut

Multiple ticket tiers, "first N free then paid," sliding-scale, non-USD,
sales tax (Stripe Tax later), seat selection. One Event = one price.

### Subtle interaction — location fuzzing

`PRD-v0.md` fuzzes Event location pre-Commit. For paid Events nobody will
pay $20 to commit before knowing where it actually is. **Recommendation:**
paid Events show neighborhood-level location pre-Commit (current fuzz
behavior is fine), exact location reveals on payment success rather than
on Commit. Same primitive, different trigger.

### Why scaffold-in-v0, launch-in-v0.5

Stripe Connect onboarding, refund flows, dispute handling, a new Commit
branch, host-cancellation forking ADR 0001 — meaningful surface area for
an app pre-validation. Schema + Connect onboarding land in v0 behind a
feature flag so flipping it on later requires no migration and no rewrite
of the Commit flow.

---

## Direction 2 — Spur+ subscription

**Status:** recommended for *entitlement-scaffolding-in-v0, launch-post-v0*.
Often the same human is both Attendee and Host, so a single subscription
unlocks both sides.

**Naming bonus:** "Spur+" reads as "Spurt" → "Growth Spurt." Lean in.

**Hard rule:** paid features may not bias trust signals or who-sees-whom.
Logistics, scheduling, history, analytics — yes. Discovery preference, rep
boost, verification skip, queue-jump — no. This keeps Spur+ orthogonal to
the moat.

### Attendee-side features

**Multi-city home + travel cities.** Free tier locks notifications and the
default map center to a single home city; Spur+ lets you add travel cities
so when you're in Austin for a week you start seeing Austin Events without
manually changing context. Implementation: a `user_locales` join table
with home/travel flag + active-window dates, queried by the
notification fan-out job. Useful for the slice of users who travel often
enough that the rebuild-your-feed friction matters but rare enough that it
doesn't earn space in the free tier.

**Calendar sync.** Free tier shows Committed Events inside the app with
push reminders; Spur+ writes them to the system calendar (iOS EventKit) so
they appear alongside work meetings and dentist appointments, with
auto-removal on Withdraw. Implementation: EventKit permission prompt on
first toggle, calendar-event ID stored on the Commit row, sync job on
Commit/Withdraw/Event-edit. The genuine value is that an Event you can't
see when scheduling other things effectively doesn't exist — calendar
sync is what makes Spur an actual scheduling primitive instead of a
notification source.

**Saved/named filter sets.** Free tier exposes the basic distance + time +
rep filters per session; Spur+ lets you save combinations as named
profiles like "after-work weekday" or "Saturday morning runs" and switch
between them in a tap. Implementation: a `saved_filters` table per user,
JSON blob of filter state + name. The wedge is that any user with two
distinct Spur use-cases (e.g., morning runs *and* evening drinks)
currently has to reset filters each time, which is exactly the user most
likely to convert.

**Richer notification rules.** Free tier sends notifications based on
simple distance + recency; Spur+ unlocks compound conditions ("rep ≥ 3.5
AND within 1mi AND host verified AND weekday evenings"). Implementation:
same `saved_filters` mechanism but with a `notify_on_match` flag,
evaluated against new-Event events from the existing fan-out path.
Corrosion check: this affects *what notifications you receive*, not *who
appears on the map* — discovery stays equal-opportunity, only the push
channel narrows.

**Personal Spur history beyond a recent window.** Free tier shows recent
Events (e.g., last 30 days); Spur+ shows your full Commit history, with
search and per-Host filtering. Implementation: trivial — the data already
exists in `commits`, this is purely a UI gate. Note that this is
*self-history only* — `CONTEXT.md:202` is explicit that other users'
event history is never shown, and that no-snooping rule must hold for
Spur+ subscribers too.

### Host-side features

**Recurring Events.** Free tier supports one-shot Events only; Spur+ Hosts
can define a recurrence rule (weekly Tuesday 7pm, monthly first Saturday)
and the system materializes the next N instances ahead. Implementation: a
`recurrence_rule` on the Event template plus a job that generates concrete
Events 2–4 weeks out, with edit-propagation rules (edit-this-only vs
edit-future). This is the single largest pull for Hosts who run actual
repeating things — run clubs, book clubs, language exchanges — and it's
also the cleanest "the app pays for itself in 30 seconds" pitch.

**Waitlist management.** Free tier hard-stops Commits when an Event hits
its cap; Spur+ Hosts can enable a waitlist where additional users queue,
get auto-promoted on a Withdraw, and receive a notification with a short
window to confirm. Implementation: `event_waitlist` table with position +
state (waiting | offered | accepted | expired), promotion job triggered on
Withdraw or capacity change. Interacts cleanly with the rep system —
promotion order is FIFO, not rep-weighted, so paying for Spur+ doesn't
buy you a queue-jump (corrosion check holds).

**Post-Event group chat persistence.** Free tier closes the chat at Event
end + a short window (e.g., 24h); Spur+ Hosts can keep it open
indefinitely so the dinner-club thread becomes the *space* the
dinner-club lives in. Implementation: a `chat_lifetime` setting on the
Event, default short-window for free, configurable for Spur+ Hosts. This
is the feature most likely to drive Host → Spur+ conversion because it
converts Spur from "I find Events on it" to "the people I met on Spur are
still in my pocket."

**Host analytics.** Free tier shows Hosts the Committed list and the rep
summary per Event; Spur+ adds a Host dashboard with show-up rate over
time, repeat-Attendee counts, rep distribution of Committed users, and
Withdraw-rate trends. Implementation: a read-side aggregator
(materialized view or Go-side query layer) over `commits` + `checkins`.
Corrosion check: analytics about *one's own past Events* is fine; this
must not become a surface for Hosts to view individual Attendee profiles
outside what `CONTEXT.md:202` already exposes.

**Compound gating rules.** Free tier supports a single rule like "rep ≥ X"
or "friends only" per Event; Spur+ unlocks AND/OR composition (e.g.,
"rep ≥ 3.5 AND verified AND not first-time-on-Spur"). Implementation: a
small rule grammar evaluated server-side in the Go layer at Commit time
(per `CLAUDE.md` business-logic-in-Go rule). Worth noting that
gender-based gating stays out of scope until gender verification ships
(`PRD-v0.md:311`) regardless of subscription tier.

### Cosmetic (both sides)

**Profile flair.** Free tier ships the v0 default minimalist profile;
Spur+ unlocks banner image, accent color, custom pronouns presentation,
and possibly an animated avatar frame — Discord-flavored, per
`CONTEXT.md:202`. Implementation: nullable cosmetic fields on the user
profile, gated by entitlement check on render. Cosmetic-only is
load-bearing — none of these surface above other users in lists, on the
map, or in trust signals; flair signals identity, not status.

### Pricing intuition

$4.99/mo or $39.99/yr. Reference: Strava is $11.99/mo for a utility-first
social app; Spur+ should be lower because the userbase is smaller and the
value is narrower. Lifetime / founder pricing for the first cohort worth
considering as a v0 thank-you and to seed early word-of-mouth.

### Why entitlement-scaffolding-in-v0, launch-post-v0

At v0 personal-project scale (few hundred users, $4.99/mo) Spur+ probably
nets <$500/mo even with strong conversion — and burns engineering on
StoreKit 2, server-side receipt validation, entitlement gating, and
restore-purchases UX that doesn't validate the wedge. Ship the
entitlement scaffolding (so paid features can be flagged on per-user)
in v0; launch Spur+ alongside paid Events post-v0.

---

## Direction 3 — Paid ID verification (deferred, but aligned)

The earlier brainstorm raised "optional ID verification fee at cost"
(Persona / Stripe Identity pass-through). This isn't redundant with v0's
existing verification because **v0 only does phone + Apple-native light
selfie liveness** ([ADR 0007](../adr/0007-apple-native-liveness-v0.md),
`PRD-v0.md:130`). Government-ID upload is **explicitly out of scope for
v0** (`PRD-v0.md:325`); third-party passive liveness is also deferred
(`PRD-v0.md:326`).

So the paid-ID-verification monetization angle is the natural funding
mechanism for the v1 verification tier *already named in the PRD as
deferred*. **Verification is a floor, not a boost** — verified users
don't outrank reputation, they just clear a higher baseline.

Not for v0. Capture and revisit when the v1 verification upgrade is
scoped.

---

## Direction 4 — Enterprise tier (post-v0, separate product surface)

Real shape — venue partnerships, professional-Host accounts, branded
Events, API access for community organizers, bulk ticketing for venues.
Held entirely until post-v0: B2B sales motion, custom contracts,
integrations are a fundamentally different product surface that shouldn't
shape v0 architecture. Separate scoping conversation once the consumer
wedge is validated.

---

## Things explicitly rejected

- **Boosted ranking on the main map/feed.** Directly converts the trust
  signal into a purchasable signal.
- **Ad-supported feed.** Same corrosion vector at higher volume.
- **Paid verification badges that substitute for earned reputation.**
  Breaks the moat.
- **Paid "see who Committed" / queue-jump on waitlists.** Trades fairness
  for revenue.

---

## Open decisions

To resume this conversation, the unresolved calls are:

1. **Take rate %** — recommended 8%. User has not explicitly confirmed.
2. **Paid Events ship timing** — recommended scaffold-in-v0,
   launch-in-v0.5. User has not explicitly confirmed.
3. **Spur+ ship timing** — recommended entitlement-scaffolding-in-v0,
   launch-post-v0. User has not explicitly confirmed.
4. **Refund policy** — recommended platform-default with no Host
   customization in v0. User has not explicitly confirmed.
5. **Whether to capture any of this as a GitHub issue or promote to ADR.**
   None promoted yet.

Once locked, the natural follow-ups are:
- ADR for *paid Hosts can cancel* (forks ADR 0001).
- ADR for Stripe Connect Express onboarding flow.
- Issue(s) for v0 schema scaffolding (paid Events columns, entitlement
  table) so launch-time flip-on requires no migration.
