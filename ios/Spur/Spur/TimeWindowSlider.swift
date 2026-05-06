import SwiftUI

// Single-thumb slider since "from" is conceptually pinned at "Live now"
// — the visible window only grows or shrinks at its tail.
struct TimeWindowSlider: View {
  @Binding var hoursAhead: Double
  let maxHours: Double
  var onEditingChanged: (Bool) -> Void = { _ in }

  var body: some View {
    VStack(spacing: 6) {
      HStack {
        Text("Live now")
          .font(.caption.weight(.semibold))
        Spacer()
        Text(label(for: hoursAhead))
          .font(.caption)
          .monospacedDigit()
          .foregroundStyle(.secondary)
      }

      Slider(
        value: $hoursAhead,
        in: 0.5...maxHours,
        step: 0.5,
        onEditingChanged: onEditingChanged
      )
      .tint(Color.accentColor)
      .accessibilityIdentifier("timeWindow.slider")
    }
    .padding(.horizontal, 14)
    .padding(.vertical, 10)
    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    .padding(.horizontal, 16)
  }

  private func label(for hours: Double) -> String {
    if hours < 1 {
      return "next \(Int((hours * 60).rounded())) min"
    }
    let totalHalfHours = Int((hours * 2).rounded())
    let days = totalHalfHours / 48
    let remHalfHours = totalHalfHours - days * 48
    let wholeHours = remHalfHours / 2
    let halfHour = remHalfHours % 2 == 1
    var parts: [String] = []
    if days > 0 { parts.append("\(days) day\(days == 1 ? "" : "s")") }
    if wholeHours > 0 || halfHour {
      let hoursLabel = halfHour ? "\(wholeHours)½ hr" : "\(wholeHours) hr"
      parts.append(hoursLabel)
    }
    return "next " + parts.joined(separator: " ")
  }
}
