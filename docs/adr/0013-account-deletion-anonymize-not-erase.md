# Account deletion: anonymize, don't erase

On account deletion, the system **hard-deletes PII and identifying traces** (phone, name, selfie, DOB, device tokens, geolocation samples) and **anonymizes references** in retained system data (outcome rows, flags they cast, chat messages, post-Tip β-Events). User identity is severed; system bookkeeping that other users depend on (reputation math, chat continuity, post-Tip event integrity) is preserved with attribution replaced by a single shared "(deleted user)" tombstone. No grace period — deletion is immediate.

## Why

The naive options both fail:

- **Hard-delete everything (CASCADE).** Destroys load-bearing data: cancels future Events that other users were counting on, erases flags the user cast (creating a rep-laundering loophole — bad actors delete and unwind their own flagging history, harming users who relied on the system for protection), erases outcome rows that drive other users' rep math, breaks chat context for surviving Attendees.
- **Preserve verbatim with user_id intact.** Privacy violation; not GDPR-compliant; violates the user's stated intent to leave.

Anonymization threads the needle: PII goes; the *facts* the system needs to keep functioning (someone showed up here, someone flagged this person, someone said this in chat) survive as anonymous bookkeeping. GDPR's Article 17 right-to-erasure applies to "personal data concerning him or her" — genuinely anonymized data is out of scope. CCPA/CPRA and US state laws are similar.

The asymmetry of preserved-vs-erased flags is intentional: **flags the user cast on others persist** (so deletion can't unwind effects on flagged users), while **flags cast on the deleted user become moot** (their rep is gone; nothing to score). The receiver always keeps what was done to them; the sender's deletion doesn't revise history for the receiver.

## Disposition by artifact

| Artifact | Behavior |
|---|---|
| PII (name, selfie/avatar, phone, DOB) | Hard delete |
| Device tokens | Hard delete |
| Geolocation traces (passive GPS samples, check-in coordinates) | **Hard delete** — identifying even without user_id |
| User row | Retained as a single shared "(deleted user)" tombstone for FK targets |
| Future α-Events they Hosted | Hard-cancel; notify active Attendees |
| Future β-Events pre-Tip | Hard-cancel; notify Committed Attendees |
| Future β-Events post-Tip | **Preserve** — Tip is sticky, the collective is bound; anonymize Seeder |
| Past Events (Done / Cancelled) | Preserve; anonymize attribution |
| Commits they made | Anonymize sender; outcome row stays |
| Outcome records (Show / Withdraw / Flake / Ghost) | Preserve — drives others' rep math |
| Flags they cast | **Preserve** — deletion doesn't unwind effects on recipients |
| Flags cast on them | Preserve in aggregate for audit; their rep is gone, doesn't move scores |
| Reputation row | Delete |
| Chat messages (DMs + Event chats) | Preserve content, anonymize sender at render |
| Photos | Preserve; anonymize attribution |
| Friendships (mirrored rows) | Hard-delete both directions |
| Pending friendship requests | Hard-delete |

## Considered alternatives

- **Hard-delete everything (CASCADE).** Rejected: destroys system data others depend on; creates rep-laundering loophole.
- **Soft-delete with 30-day grace period for undo.** Rejected for v0 — adds complexity without clear demand at personal-project scale. Standard polish for v1+ if user research shows regret-deletion is common.
- **Anonymize chat content too** (replace message body with `[message removed]`). Rejected — loses chat context for surviving Attendees and disrupts conversation continuity. GDPR generally accepts content retention with sender attribution stripped (industry-standard at Discord, Slack, Reddit). The deleted user's PII isn't in their typical chat content; if they wrote PII into the message body, that's content they authored and is treated as their content, not their identity.

## Consequences

- **Rep-laundering by deletion.** A user who accumulates flags can delete and re-sign up with the same phone for a fresh start. Phone is freed by deletion; new account has no link to the old. Accepted at v0 scale; closing this requires phone-hash banning (privacy/processing implications) and is gated behind distribution.
- **No rep portability.** A user who deletes and returns starts at New status with no rep, no friends, no history. This is intentional — "fresh start" is honest and avoids any cross-account identity tracking.
- **β-Event ownership post-deletion.** A post-Tip β-Event whose Seeder deletes proceeds with no Seeder. The chat continues; the Event runs; outcomes are recorded. Consistent with ADR 0001 (Seeders can't cancel) — the Event was never the Seeder's to control after Tip; deletion just makes the absence visible.
- **Friend-graph silence.** A user's Friends see their friend list shrink without notification. Optional v1 polish: a one-time "(your friend) left the app" notice.
- **GDPR/CCPA compliance** — the design satisfies both with the deletion-on-request, PII-erasure, and anonymization-of-references discipline. Geolocation hard-deletion is what brings the design over the line; without it, retained outcome+location pairs would be re-identifiable at v0 scale.
