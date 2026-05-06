import SwiftUI

// Brief value-prop screen shown the first time the user reaches the map,
// before iOS's system permission prompt fires (PRD-v0:165). The Spur custom
// copy explains *why* we're about to ask, so the system prompt has context;
// then the CTA calls into LocationManager which triggers the system prompt.
struct LocationPermissionView: View {
  let onAllow: () -> Void
  let onSkip: () -> Void

  var body: some View {
    VStack(spacing: 24) {
      Spacer()

      Image(systemName: "location.circle.fill")
        .font(.system(size: 72))
        .foregroundStyle(.tint)

      VStack(spacing: 12) {
        Text("See Spurs near you")
          .font(.title2.bold())
        Text(
          "Spur shows Events happening around you on a map. We use your location once, only while you have the app open."
        )
        .font(.body)
        .multilineTextAlignment(.center)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 24)
      }

      Spacer()

      VStack(spacing: 12) {
        Button(action: onAllow) {
          Text("Allow location")
            .font(.headline)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 12)
        }
        .buttonStyle(.borderedProminent)

        Button(action: onSkip) {
          Text("Not now")
            .font(.subheadline)
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
      }
      .padding(.horizontal, 24)
      .padding(.bottom, 32)
    }
  }
}
