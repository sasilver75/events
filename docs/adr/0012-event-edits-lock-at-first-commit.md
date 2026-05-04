# Event edits lock at first Commit; cancel-and-recreate preserves audience

After the first Commit lands on an Event, the Host can edit only **title, description, Cap (increase only), and gating rules (loosen only)**. Time, location, category, Tip threshold, and Tip deadline are locked. Material changes to a locked field require **cancel-and-recreate** — α-Hosts cancel (paying lateness-scaled rep cost per PRD user story 18) and create a new Event with a `supersedes_event_id` link. The system pushes a **recommit invite** to active Attendees of the cancelled Event (those who hadn't Withdrawn at cancellation time), pointing to the successor; the invite expires when the successor goes Live. Once Live, no edits at all.

## Why

Commit means "I'm coming to *this* thing." If "this thing" is mutable post-Commit, the binding is on shifting sand and the wedge collapses — Attendees stop trusting that what they Committed to is what will fire. The hard lock at first-Commit makes Commit meaningful.

The cancel-and-recreate path is humane to Hosts who genuinely need to change something (venue floods, time conflict). Without an audience-preservation mechanism, the system would pressure Hosts to lie about minor changes ("don't worry, same place" while quietly editing) or to absorb the cost of rebuilding a room from scratch. The recommit invite preserves the audience as a courtesy while keeping the cancellation cost honest.

The cancellation cost is **not waived** when using cancel-and-recreate. Mechanically it *is* a cancellation; the rep cost is what makes editing-by-recreate expensive enough that Hosts pin carefully the first time. Without the cost, the editing-prohibition collapses — Hosts route around it freely. Lateness-scaling makes last-minute changes most expensive of all, naturally discouraging frivolous use.

Title/description edits push to current Attendees (debounced ~5min) so people aren't surprised by content drift. Cap-increase and gating-loosening are safe because they only widen who can be there; they don't change the deal for existing Attendees.

## Considered alternatives

- **Allow location edits within a small radius** (e.g., <500m) — rejected: the line is hard to hold; "3 blocks" becomes "across the park" becomes "across town" through accretion. Cleaner to require cancel-and-recreate for any location change.
- **No cancel-and-recreate audience preservation** — rejected: punishes Hosts for honest mistakes by forcing them to rebuild a room from scratch. Without recommit-invite, the system pressures Hosts to lie about minor changes rather than be honest about needing to recreate.
- **Waive cancellation rep cost when using cancel-and-recreate** — rejected: collapses the editing-prohibition. With no cost, Hosts edit-by-recreate freely and Commit becomes unbinding.
- **Allow Hosts a single "free" edit per Event** — rejected: introduces a special case that rewards arbitrary first-edit behavior; the binary lock is cleaner.

## Consequences

- **β-Events have no editing path post-first-Commit at all** because Seeders cannot cancel (ADR 0001). β-Seeders who set wrong terms live with the failed-Tip mechanic (event Cancels if it doesn't reach Tip by deadline) or with the deal as committed. Consistent with 0001's intent.
- Hosts who frequently cancel-and-recreate accumulate rep cost mechanically — the system self-limits abuse without policing.
- A `supersedes_event_id` chain could in principle grow long (recreate → cancel → recreate → ...). At v0 scale this is not a concern; the Host's accumulating rep cost makes long chains rare.
- Recipients of a recommit invite who decline or ignore pay no rep cost — the original Event was Cancelled (not Withdrawn-from), and Cancellation is not a Flake for Attendees.
