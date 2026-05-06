import SwiftUI

struct EventDetailSheet: View {
  let event: NearbyEvent
  var onCommitStateChanged: (_ commitCount: Int, _ committedByMe: Bool) -> Void = { _, _ in }

  @Environment(AuthModel.self) private var auth

  @State private var commitCount: Int
  @State private var committedByMe: Bool
  @State private var inFlight = false
  @State private var errorMessage: String?

  init(
    event: NearbyEvent,
    onCommitStateChanged: @escaping (_ commitCount: Int, _ committedByMe: Bool) -> Void = { _, _ in
    }
  ) {
    self.event = event
    self.onCommitStateChanged = onCommitStateChanged
    _commitCount = State(initialValue: event.commitCount)
    _committedByMe = State(initialValue: event.committedByMe)
  }

  var body: some View {
    NavigationStack {
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          header
          metaRow
          commitButton
          if let errorMessage {
            Text(errorMessage)
              .font(.footnote)
              .foregroundStyle(.red)
          }
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
        primary: event.cap.map { "\(commitCount) / \($0)" } ?? "\(commitCount)",
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

  private var isFull: Bool {
    guard let cap = event.cap else { return false }
    return !committedByMe && commitCount >= cap
  }

  private var commitButton: some View {
    Button(action: { Task { await toggleCommit() } }) {
      HStack {
        if inFlight {
          ProgressView().controlSize(.small)
        }
        Text(commitButtonLabel)
          .font(.headline)
      }
      .frame(maxWidth: .infinity, minHeight: 44)
    }
    .buttonStyle(.borderedProminent)
    .tint(committedByMe ? .gray : .accentColor)
    .disabled(inFlight || isFull)
    .accessibilityIdentifier("eventDetail.commitButton")
  }

  private var commitButtonLabel: String {
    if isFull { return "Full" }
    return committedByMe ? "Withdraw" : "Commit"
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

  private func toggleCommit() async {
    let priorCount = commitCount
    let priorCommitted = committedByMe
    let willCommit = !priorCommitted

    inFlight = true
    errorMessage = nil
    committedByMe = willCommit
    commitCount = priorCount + (willCommit ? 1 : -1)

    defer { inFlight = false }

    do {
      let result =
        willCommit
        ? try await EventsAPI.commit(eventID: event.id, auth: auth)
        : try await EventsAPI.withdraw(eventID: event.id, auth: auth)
      commitCount = result.commitCount
      committedByMe = result.committedByMe
      onCommitStateChanged(result.commitCount, result.committedByMe)
    } catch EventsAPI.APIError.eventFull {
      committedByMe = priorCommitted
      // Snap the count up to cap so the button reflects the true server
      // state instead of the stale local count that motivated the attempt.
      commitCount = event.cap ?? priorCount
      onCommitStateChanged(commitCount, committedByMe)
      errorMessage = "This event is full"
    } catch {
      commitCount = priorCount
      committedByMe = priorCommitted
      errorMessage = error.localizedDescription
    }
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
