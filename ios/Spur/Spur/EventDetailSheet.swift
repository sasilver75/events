import CoreLocation
import SwiftUI

struct EventDetailSheet: View {
  let event: NearbyEvent
  var onCommitStateChanged: (_ commitCount: Int, _ committedByMe: Bool) -> Void = { _, _ in }
  var onCheckedIn: () -> Void = {}

  @Environment(AuthModel.self) private var auth

  @State private var commitCount: Int
  @State private var committedByMe: Bool
  @State private var inFlight = false
  @State private var errorMessage: String?

  // Seeded from the server-side `checked_in_by_me` projection so
  // re-presenting the sheet after dismissal reflects the persisted state
  // — without this seed, the local @State resets to false and the button
  // appears tappable again. The post-tap onCheckedIn callback updates the
  // parent's events array so the next presentation reads the right value
  // from `event.checkedInByMe` even before the next browse refetch.
  @State private var checkedIn: Bool
  @State private var checkInInFlight = false
  @State private var checkInError: String?

  init(
    event: NearbyEvent,
    onCommitStateChanged: @escaping (_ commitCount: Int, _ committedByMe: Bool) -> Void = {
      _, _ in
    },
    onCheckedIn: @escaping () -> Void = {}
  ) {
    self.event = event
    self.onCommitStateChanged = onCommitStateChanged
    self.onCheckedIn = onCheckedIn
    _commitCount = State(initialValue: event.commitCount)
    _committedByMe = State(initialValue: event.committedByMe)
    _checkedIn = State(initialValue: event.checkedInByMe)
  }

  var body: some View {
    NavigationStack {
      ScrollView {
        VStack(alignment: .leading, spacing: 0) {
          banner
          VStack(alignment: .leading, spacing: 16) {
            header
            metaRow
            commitButton
            if let errorMessage {
              Text(errorMessage)
                .font(.footnote)
                .foregroundStyle(.red)
            }
            if showCheckIn {
              checkInButton
              if let checkInError {
                Text(checkInError)
                  .font(.footnote)
                  .foregroundStyle(.red)
              }
            }
            Divider()
            description
          }
          .padding(.horizontal, 20)
          .padding(.top, 20)
          .padding(.bottom, 24)
        }
      }
      .scrollIndicators(.hidden)
    }
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
  }

  // Hero banner. Public-read bucket means a plain AsyncImage works —
  // no auth headers, no signed URLs, the Supabase CDN caches by URL.
  // When no banner is set, fall back to a category-color block so the
  // detail surface still has a hero.
  @ViewBuilder
  private var banner: some View {
    if let path = event.bannerPath, let url = BannerStorage.publicURL(forPath: path) {
      AsyncImage(url: url) { phase in
        switch phase {
        case .success(let image):
          image.resizable().scaledToFill()
        case .failure:
          bannerFallback
        case .empty:
          bannerFallback.overlay(ProgressView())
        @unknown default:
          bannerFallback
        }
      }
      .frame(height: 180)
      .frame(maxWidth: .infinity)
      .clipped()
    } else {
      bannerFallback.frame(height: 120)
    }
  }

  private var bannerFallback: some View {
    event.categoryEnum.color.opacity(0.85)
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

  // The check-in row is only meaningful for a Committed Attendee on a
  // Live Event — both must be true. Once `checkedIn` flips after a
  // successful tap, the row stays visible as a confirmation.
  private var showCheckIn: Bool {
    committedByMe && event.state == "Live"
  }

  private var checkInButton: some View {
    Button(action: { Task { await tapCheckIn() } }) {
      HStack {
        if checkInInFlight {
          ProgressView().controlSize(.small)
        }
        Image(systemName: checkedIn ? "checkmark.circle.fill" : "mappin.and.ellipse")
        Text(checkedIn ? "Checked in" : "I'm here")
          .font(.headline)
      }
      .frame(maxWidth: .infinity, minHeight: 44)
    }
    .buttonStyle(.bordered)
    .tint(checkedIn ? .green : .accentColor)
    .disabled(checkInInFlight || checkedIn)
    .accessibilityIdentifier("eventDetail.checkInButton")
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

  // tapCheckIn pulls a one-shot best-accuracy GPS fix and posts it; the
  // server applies the accuracy-aware geofence (ADR 0011). Optimistic
  // toggle on tap; revert on rejection. The probe is recreated each tap
  // so a stale delegate from a prior fix never resolves the new
  // continuation.
  private func tapCheckIn() async {
    checkInInFlight = true
    checkInError = nil
    let priorChecked = checkedIn
    checkedIn = true
    defer { checkInInFlight = false }

    do {
      let probe = CheckInLocationProbe()
      let fix = try await probe.requestFix()
      _ = try await EventsAPI.checkIn(
        eventID: event.id,
        lat: fix.coordinate.latitude,
        lon: fix.coordinate.longitude,
        horizontalAccuracyM: fix.horizontalAccuracy,
        auth: auth)
      // Permission request is gated to first check-in (PRD §31, issue #35
      // — not at app launch) so the prompt lands in the moment that
      // motivates it. Idempotent if the user already granted/denied.
      await NotificationScheduler.requestAuthorizationIfNeeded()
      await NotificationScheduler.scheduleEndOfEventReminder(
        eventID: event.id,
        eventTitle: event.title,
        endTime: event.endTime)
      onCheckedIn()
    } catch let error as EventsAPI.APIError {
      checkedIn = priorChecked
      checkInError = error.errorDescription
    } catch {
      checkedIn = priorChecked
      checkInError = error.localizedDescription
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
