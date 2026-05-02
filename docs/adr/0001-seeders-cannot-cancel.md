# Seeders cannot cancel β-Events

A **Seeder** of a β-(Seeded) Event has no power to cancel the Event they created. They may only withdraw their own **Commit**, which decrements the Tip count like any other Attendee's withdrawal. β-Events terminate only via failed-Tip (deadline reached without threshold), un-Tip (post-Tip withdrawals drop count below threshold), or platform action.

## Why

The defining philosophical move of the β-Event shape is that the Seeder is *not* the owner — they propose a coordination, and the resulting Event belongs to the collective of Committed Attendees. Letting the Seeder cancel would silently collapse β back into α (a Host-owned event with extra steps).

Three concrete consequences:

1. **Seeding feels safe.** Users propose without weighing "what if I get cold feet later?" — they can always just withdraw their own Commit.
2. **Collective ownership is real, not rhetorical.** Once strangers Commit, they have stake. The proposer cannot unilaterally erase that.
3. **A class of grief vectors disappears.** Seeders cannot bait-and-switch (let the count climb, then yank), nor cancel out of spite.

## Considered alternatives

- **Seeder-can-cancel** — rejected: collapses β into α, introduces grief vectors, makes seeding feel risky.
- **Group vote to cancel** — rejected as v1 complexity (UI, quorum rules, edge cases). May revisit if collective Cancel becomes a real need.
- **Seeder withdrawal auto-cancels the Event** — rejected: privileges the Seeder's role after we just argued it shouldn't be privileged. A Seeder withdrawing should be ordinary attrition.

## Consequences

- α-Events and β-Events are *structurally* asymmetric on cancellation rights, not just stylistically. This asymmetry must be made visible in the UI so users understand the trade when choosing which shape to create.
- Hosts of α-Events retain cancel power (they hold accountability; they get the verb). Cancellation by a Host carries reputation cost on a gradient (lighter >24hr out, heavier <24hr out).
- Platform/admin retains the ability to cancel any Event for safety or policy reasons; this never affects user reputation.
