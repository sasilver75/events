# Attendance is resolved by geofence-gated check-in, not peer attestation

The platform resolves "did this Attendee Show or Ghost?" using **GPS-gated self-check-in** combined with **post-event peer flags from ratings**, not by asking Hosts or peers to directly attest who was physically present. Check-in taps are rejected unless the user's location places them within a small radius (~50m) of the Event pin at tap time.

## Why

The no-snooping principle applies to attendance as much as to event-list browsing. Asking a Host to mark "Sarah was here, Mike was not" turns Hosts into surveillance proxies — exactly the dynamic the rest of the privacy model is designed to avoid. Objective questions (was someone physically there?) should be answered by objective signals (GPS). Subjective signals (peer flags, ratings) stay in the subjective lane (was the experience good? was anyone concerning?).

Geofence-gating the tap is anti-fraud (you can't claim presence remotely) and computationally cheap (one location query at tap time, no continuous polling).

## Considered alternatives

- **Host or peer attestation as primary signal** — rejected: violates the no-snooping principle and turns peers into surveillance proxies.
- **Continuous geofence during Live** — deferred to v1: more accurate (catches early-departure) but introduces battery drain, indoor-accuracy issues, and engineering complexity that v0 doesn't need.
- **Trust ratings/flags only, no check-in** — rejected: loses real-time presence signal needed for in-event affordances (chat features, panic button, slot recycling for known absences).

## Consequences

- "Soft Ghost" emerges as a distinct disposition: didn't check in, no peer flag either. Carries a light reputation ding to incentivize the tap, but is not punished as harshly as an explicit Ghost.
- The "I was there but forgot to tap" failure mode produces false soft-Ghosts. Acceptable cost for v0; mitigated when v1 adds continuous geofence.
- The geofence radius is a calibration knob — too tight and indoor-accuracy issues create false rejections; too loose and fraud becomes possible. ~50m is the v0 starting point.
