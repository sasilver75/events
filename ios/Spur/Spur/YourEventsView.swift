import SwiftUI

// YourEventsView is the bottom-nav "Your Events" surface (PRD US 45/46) —
// a two-section list of the caller's upcoming and recent Commits. Tapping a
// row hands the event to AppNavigation, which switches to the Map tab and
// asks ContentView to recenter on the pin and present its EventDetailSheet.
struct YourEventsView: View {
  @Environment(AuthModel.self) private var auth
  @Environment(AppNavigation.self) private var nav

  @State private var upcoming: [EventsAPI.MyCommitEvent] = []
  @State private var recent: [EventsAPI.MyCommitEvent] = []
  @State private var loadError: String?
  @State private var hasLoaded = false

  var body: some View {
    NavigationStack {
      Group {
        if let err = loadError, !hasLoaded {
          ContentUnavailableView(
            "Couldn't load your events",
            systemImage: "exclamationmark.triangle",
            description: Text(err))
        } else if hasLoaded && upcoming.isEmpty && recent.isEmpty {
          ContentUnavailableView(
            "No Commits yet",
            systemImage: "calendar",
            description: Text("Commit to an Event on the Map and it'll show up here."))
        } else {
          list
        }
      }
      .navigationTitle("Your Events")
      .refreshable { await reload() }
      // Refetch on every appear (tab switch, return from background) so a
      // Commit made elsewhere — Map's commit button, a friend's invite —
      // surfaces without a manual pull-to-refresh. .task auto-cancels on
      // disappear, so an in-flight fetch from a previous appearance can't
      // race the new one's state writes.
      .task { await reload() }
    }
  }

  private var list: some View {
    List {
      if !upcoming.isEmpty {
        Section("Upcoming") {
          ForEach(upcoming) { event in
            row(event)
          }
        }
      }
      if !recent.isEmpty {
        Section("Recent") {
          ForEach(recent) { event in
            row(event)
          }
        }
      }
    }
    .listStyle(.insetGrouped)
  }

  private func row(_ event: EventsAPI.MyCommitEvent) -> some View {
    Button {
      nav.openEvent(event)
    } label: {
      HStack(alignment: .top, spacing: 12) {
        CategoryGlyph(category: event.categoryEnum)
        VStack(alignment: .leading, spacing: 4) {
          Text(event.title)
            .font(.body.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(2)
          Text(timeLabel(start: event.startTime, end: event.endTime))
            .font(.caption)
            .foregroundStyle(.secondary)
          StateBadge(state: event.state)
        }
        Spacer(minLength: 0)
        Image(systemName: "chevron.right")
          .font(.caption.weight(.semibold))
          .foregroundStyle(.tertiary)
      }
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .accessibilityIdentifier("yourEvents.row.\(event.id)")
  }

  private func reload() async {
    do {
      let result = try await EventsAPI.fetchMyCommits(auth: auth)
      upcoming = result.upcoming
      recent = result.recent
      loadError = nil
      hasLoaded = true
    } catch is CancellationError {
      return
    } catch {
      loadError = error.localizedDescription
      hasLoaded = true
    }
  }

  // Format: "Tue 7:30–9:00 PM" or "Today 2:00 PM". Calendar-aware so
  // crossing-midnight Events don't read as the same time on both sides.
  private func timeLabel(start: Date, end: Date) -> String {
    let cal = Calendar.current
    let now = Date()
    let dayFmt = DateFormatter()
    dayFmt.dateFormat = "EEE"
    let timeFmt = DateFormatter()
    timeFmt.timeStyle = .short
    timeFmt.dateStyle = .none

    let dayLabel: String
    if cal.isDateInToday(start) {
      dayLabel = "Today"
    } else if cal.isDateInTomorrow(start) {
      dayLabel = "Tomorrow"
    } else if cal.isDateInYesterday(start) {
      dayLabel = "Yesterday"
    } else if abs(start.timeIntervalSince(now)) < 7 * 24 * 3600 {
      dayLabel = dayFmt.string(from: start)
    } else {
      let dateFmt = DateFormatter()
      dateFmt.dateFormat = "MMM d"
      dayLabel = dateFmt.string(from: start)
    }

    if cal.isDate(start, inSameDayAs: end) {
      return "\(dayLabel) \(timeFmt.string(from: start))–\(timeFmt.string(from: end))"
    }
    return "\(dayLabel) \(timeFmt.string(from: start))"
  }
}

private struct CategoryGlyph: View {
  let category: EventCategory
  var body: some View {
    ZStack {
      Circle()
        .fill(category.color.opacity(0.18))
      Image(systemName: category.symbolName)
        .font(.subheadline.weight(.semibold))
        .foregroundStyle(category.color)
    }
    .frame(width: 36, height: 36)
  }
}

private struct StateBadge: View {
  let state: String
  var body: some View {
    Text(state)
      .font(.caption2.weight(.semibold))
      .padding(.horizontal, 8)
      .padding(.vertical, 3)
      .background(background, in: Capsule())
      .foregroundStyle(foreground)
  }

  private var background: Color {
    switch state {
    case "Live": return .yellow.opacity(0.25)
    case "Tipped", "Filling": return .green.opacity(0.18)
    case "Done": return .gray.opacity(0.18)
    case "Cancelled": return .red.opacity(0.18)
    default: return .secondary.opacity(0.18)
    }
  }

  private var foreground: Color {
    switch state {
    case "Live": return .orange
    case "Tipped", "Filling": return .green
    case "Done": return .secondary
    case "Cancelled": return .red
    default: return .secondary
    }
  }
}
