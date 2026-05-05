# Twilio Verify (not Programmable Messaging + A2P 10DLC) for SMS OTP delivery

Hosted Spur Auth (`spur-staging`, eventually prod) configures Supabase's **Twilio Verify** provider — Account SID + Auth Token + Verify Service SID. Twilio generates and verifies the OTP code; Supabase delegates to it and issues the JWT after a successful verify. We do not register an A2P 10DLC brand and campaign, and we do not use Twilio Programmable Messaging. The original Phase 2 brief in [#9](https://github.com/sasilver75/events/issues/9) specced 10DLC; this ADR is the corrected decision.

## Why

Three forces all point the same way at v0 scale, and only one of them reverses at much higher scale.

**Cost shape.** Twilio Verify is $0.05 per verification with no monthly minimum. Programmable Messaging on a US 10DLC long code is ~$0.005 per SMS plus ~$1.15/month for the number, plus one-time fees with The Campaign Registry (TCR) — $4 brand registration + $10–15 campaign registration — plus a recurring $1.50–15/month campaign fee that you pay even with zero traffic. At v0's "few hundred users" estimate, with at most a few hundred lifetime verifications and well under a hundred per month, Verify totals roughly $25/year and 10DLC totals $30–200/year, with the variance driven entirely by 10DLC's fixed monthly fees. The break-even where 10DLC's lower per-message rate overcomes its base costs is around 5,000 verifications/month, which v0 will not hit.

**Setup speed.** Verify is a single dashboard flow — provision a Verify Service, capture the SID, paste into Supabase Auth — measurable in minutes. 10DLC requires registering a brand identity with TCR, then registering a campaign with sample messages, and waiting on carrier vetting (Verizon, AT&T, T-Mobile each review independently). Lead time is typically 1–7 days, sometimes longer for higher-throughput tiers. The maintainer's stated goal for Phase 2 was "today"; 10DLC cannot meet that, and there is no v0 reason for it to need to.

**International reach.** The country picker shipped in iOS during #9 Phase 1 lists ~245 countries because Spur is in LA, which has tourism. 10DLC is US-only by definition — every other jurisdiction has its own SMS regulatory framework. Twilio Verify routes correctly across ~200 countries with one configuration. Programmable Messaging international is a separate layer of work (provider lookups, per-country compliance, sender-ID rules) that Verify subsumes.

**Fraud protection** sits adjacent to the other three: Verify includes per-number rate limiting, suspicious-routing detection, and abuse heuristics. With Programmable Messaging we would build those defenses or accept their absence.

The one force pointing the other way is **per-message cost at scale**. If Spur ever does ~10K verifications/month, Verify's ~$500/month bill exceeds 10DLC's, and the migration calculus changes. That migration is a config flip in the Supabase Auth dashboard (`twilio_verify` → `twilio`) plus completing TCR registration. **The iOS code, the Go JWKS middleware, the JWT shape, and the database schema are unchanged.** The migration's cost is wall-clock days waiting on carrier review, not engineering.

## Shape

- **Provider.** `twilio_verify` in Supabase Auth's hosted dashboard for `spur-staging`. Supabase delegates to Twilio Verify's `Verifications.create` endpoint with the user's E.164 phone, then to `VerificationChecks.create` when iOS submits the code. Supabase still mints the session JWT; the JWKS-verification path on Spur's Go server is unaffected.
- **Credentials.** Account SID, Auth Token, **Verify Service SID** (one Verify Service per use case; for v0 we use one named "Spur sign-in OTP"). The Auth Token never leaves the Supabase dashboard — Spur's Go server and iOS client never see it. Per [ADR 0014](./0014-secret-management.md), this is correct: the Auth Token grants access to shared Twilio infrastructure, so it lives in exactly one place.
- **Local development unchanged.** `supabase/config.toml`'s `[auth.sms.test_otp]` map continues to short-circuit OTP locally without contacting Twilio. The local `[auth.sms.twilio]` stub credentials in the same file are unused but required syntactically while the SMS provider section is enabled.
- **iOS client unchanged.** The country-picker → E.164 → `signInWithOTP(phone:)` → `verifyOTP(phone:token:type:.sms)` flow is provider-agnostic; the supabase-swift SDK call is identical regardless of which SMS provider Supabase is configured with.
- **Go middleware unchanged.** JWT validation is JWKS-based, signed by Supabase's keys; the issuer is `<supabase-url>/auth/v1` regardless of which provider delivered the SMS. No conditional code paths.

## Considered alternatives

- **Twilio Programmable Messaging + A2P 10DLC long code.** Original Phase 2 spec in #9. Cheapest per message at scale; loses on setup time, base costs at v0 traffic, international reach, and built-in fraud protection. Becomes the right tool above ~5K verifications/month — see migration note above.
- **Twilio Programmable Messaging + Toll-Free Number.** Faster to provision than 10DLC (hours, not days), still US-only, comparable per-message cost. Loses on the same international and fraud-protection axes. No reason to pick it over Verify at v0.
- **MessageBird, Vonage, TextLocal.** All supported by Supabase. None are categorically cheaper than Twilio Verify at v0 scale, and adopting one introduces a second vendor with no offsetting benefit. Twilio also covers voice, WhatsApp, and email if any become relevant later; the others don't.
- **Twilio Verify TOTP (authenticator-app codes) instead of SMS.** Skips carrier issues entirely. Rejected because phone-OTP is what users expect from a stranger-meeting product, and possession-of-phone is part of the trust signal Spur's reputation system depends on per PRD-v0. TOTP proves possession of a device, not a phone number, which is the wrong signal for this product.
- **Build OTP ourselves: random code, store in Postgres, deliver via Programmable Messaging.** Strictly more code, more attack surface (rate limiting, code-reuse policy, expiry handling, brute-force defenses all become our problem), no fraud protection, no internationalization. GoTrue exists precisely to absorb this work; building it would reverse [ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md)'s posture.
- **Email-link OTP only, skip SMS.** Out of scope per #9 and PRD-v0. Email is a separate identity channel with its own constraints (deliverability, universal-link handling on iOS) and doesn't replace phone for the trust signal.

## Consequences

- **Phase 2 of #9 changes shape.** Original spec: Twilio Messaging Service + A2P 10DLC registration. Corrected Phase 2: Twilio account → Verify Service → capture Verify Service SID → paste into Supabase staging Auth dashboard. The handoff comment on #9 supersedes the issue body's Phase 2 substeps; the body should be updated when convenient, but agents picking up the issue should treat the comment + this ADR as canonical.
- **Cost ceiling tied to traffic.** Per-verification billing means a runaway-signup attack could spike the Twilio bill before we notice. Mitigation has two layers: Verify's built-in per-number / per-IP rate limiting, plus a Twilio account spending alert (~$50/month) set when staging goes live. At v0 traffic the alert should never fire; if it does, we investigate.
- **Provider migration is a config change, not a code change.** If/when Spur crosses ~5K verifications/month, switch from `twilio_verify` to `twilio` in the Supabase dashboard, complete TCR registration (allow days), provision a 10DLC long code, paste the new credentials. iOS, Go, JWKS, schema all unchanged.
- **No A2P 10DLC pre-work to track.** Spur does not have a TCR brand or campaign and is not in any carrier registration queue. If we ever need A2P, we register fresh.
- **`supabase/config.toml` semantics.** Local `[auth.sms.twilio]` stub credentials remain because Supabase requires them syntactically while `[auth.sms]` is enabled, even though `[auth.sms.test_otp]` short-circuits the actual provider call. Hosted staging configures `twilio_verify` in the dashboard, which is independent of `config.toml`'s local-only fields.
- **Distribution gate.** Before non-self distribution, revisit: rate-limit thresholds, account-level spending caps, per-country routing audit (e.g. block expensive routes if not used), and whether the Verify SLA is sufficient for the trust posture.
