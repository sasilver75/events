import SwiftUI

// Two-detent bottom sheet for an Event tapped on the map. Peek (medium) shows
// the headline metadata; expanded (large) scrolls into the description and
// any further detail. No Commit button yet — that arrives in issue #11 along
// with the exact-location reveal for Committed Attendees.
struct EventDetailSheet: View {
  let event: NearbyEvent

  var body: some View {
    NavigationStack {
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          header
          metaRow
          Divider()
          description
        }
        .padding(.horizontal, 20)
        .padding(.top, 28)
        .padding(.bottom, 24)
      }
      .scrollIndicators(.hidden)
    }
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
  }

  private var header: some View {
    HStack(alignment: .top, spacing: 12) {
      Image(systemName: event.categoryEnum.symbolName)
        .font(.title2)
        .foregroundStyle(.white)
        .frame(width: 44, height: 44)
        .background(event.categoryEnum.color, in: Circle())

      VStack(alignment: .leading, spacing: 4) {
        Text(event.title)
          .font(.title3.bold())
        Text(event.category)
          .font(.subheadline)
          .foregroundStyle(.secondary)
      }
      Spacer(minLength: 0)
    }
  }

  private var metaRow: some View {
    HStack(spacing: 24) {
      metaCell(
        icon: "calendar",
        primary: Self.dateLine(event.startTime),
        secondary: Self.timeLine(event.startTime))
      metaCell(
        icon: "person.2.fill",
        primary: event.cap.map { "\(event.commitCount) / \($0)" } ?? "\(event.commitCount)",
        secondary: "Committed")
    }
  }

  private func metaCell(icon: String, primary: String, secondary: String) -> some View {
    HStack(spacing: 8) {
      Image(systemName: icon)
        .foregroundStyle(.secondary)
      VStack(alignment: .leading, spacing: 2) {
        Text(primary).font(.subheadline.weight(.medium))
        Text(secondary).font(.caption).foregroundStyle(.secondary)
      }
    }
  }

  private var description: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text("About")
        .font(.headline)
      Text(Self.renderMarkdown(event.description))
        .font(.body)
        .foregroundStyle(.primary)
    }
  }

  // Hosts write descriptions in Markdown; the server stores plain text.
  // Inline-only-preserving-whitespace keeps bold/italic/links and newlines
  // (so `- item` lines stay on their own line) without pulling in block
  // layout that SwiftUI's `Text` can't fully render.
  static func renderMarkdown(_ raw: String) -> AttributedString {
    let options = AttributedString.MarkdownParsingOptions(
      interpretedSyntax: .inlineOnlyPreservingWhitespace)
    if let parsed = try? AttributedString(markdown: raw, options: options) {
      return parsed
    }
    return AttributedString(raw)
  }

  private static func dateLine(_ d: Date) -> String {
    let f = DateFormatter()
    f.dateStyle = .medium
    f.timeStyle = .none
    return f.string(from: d)
  }

  private static func timeLine(_ d: Date) -> String {
    let f = DateFormatter()
    f.dateStyle = .none
    f.timeStyle = .short
    return f.string(from: d)
  }
}
