# Check-in geofence is accuracy-aware, not fixed-radius

The check-in tap is accepted when the user's reported location, expanded by iOS's `horizontalAccuracy` reading, overlaps the 50m geofence — i.e., `distance_to_pin - horizontalAccuracy <= 50m`. A tap from a phone reporting ±80m accuracy at 100m from the pin is accepted; a tap from a phone reporting ±10m accuracy at 60m is rejected.

## Why

Indoor venues degrade GPS to 50–200m on iPhone (WiFi triangulation, stale fixes, building offsets). A fixed 50m radius silently Ghosts honest indoor Attendees who declined "Always Allow" — they did everything right but their sensor couldn't pinpoint them. iOS *tells* us when its fix is uncertain; rejecting a user whose phone admits it can't pinpoint them is punishing them for sensor honesty.

The accuracy-aware rule biases toward false-positives where the device is honestly uncertain (indoor / urban canyon) while preserving the 50m anti-fraud floor where the fix is confident (outdoor / clear sky). The spoof zone does not generally widen — to claim presence from 200m away, you'd need a phone reporting ±150m accuracy, which clear-sky GPS doesn't produce.

## Considered alternatives

- **Fixed 50m + telemetry-driven calibration** — rejected: ships with a known miss for the slice (declined Always Allow + indoor venue), with the cost paid in unearned Ghosts during the calibration period.
- **Category-aware default (100m indoor / 50m outdoor)** — rejected: requires venue classification (Hosts misclassify; "Food" spans patios + restaurants); doubles the spoof zone uniformly for indoor venues regardless of actual GPS quality.

## Consequences

- The passive 150m radius (ADR 0009) is unchanged — passive is anti-fraud-strong by being background-only and absorbs most permission-granted indoor cases regardless.
- A spoofer with a jailbroken device that fakes `horizontalAccuracy` could widen their effective claim radius. v0 accepts this — at v0 scale and rep gates, rep-farming via spoofed accuracy is not a cost-effective attack.
- Tap-rejection telemetry by category remains valuable for tuning the 50m floor itself, even with accuracy-awareness layered on top.
