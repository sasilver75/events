import SwiftUI

// FeedbackSheet captures the post-event flow: 👍 / 👎 / skip per fellow
// Show-outcome Attendee, with a "what happened?" reasons sheet on 👎
// (3 hard reasons + 1 soft, per ADR 0008). Submit posts the batch
// to POST /events/{id}/feedback and dismisses on success.
//
// Pre-fill: GET /events/{id}/feedback returns any prior submissions so
// reopen-and-edit works in-place. The user can switch a 👎 → 👍 (which
// removes the flag server-side) or change the set of reasons; the batch
// submit overwrites previous signals atomically.
struct FeedbackSheet: View {
  let eventID: String

  @Environment(AuthModel.self) private var auth
  @Environment(\.dismiss) private var dismiss

  @State private var loaded = false
  @State private var loadError: String?
  @State private var targets: [EventsAPI.FeedbackTarget] = []
  // Map keyed by target user_id. nil entry = "haven't decided yet".
  @State private var selections: [String: Selection] = [:]
  // Target whose reasons sheet is open, if any.
  @State private var reasoningFor: String?
  @State private var submitting = false
  @State private var submitError: String?

  // Selection bundles a per-target signal + reasons. The sheet stores
  // the full set so resubmission can clear reasons when the user
  // switches 👎 → 👍.
  struct Selection: Equatable {
    var signal: String
    var reasons: Set<String>

    static let up = Selection(signal: "up", reasons: [])
    static let skip = Selection(signal: "skip", reasons: [])
    static func down(reasons: Set<String> = []) -> Selection {
      Selection(signal: "down", reasons: reasons)
    }
  }

  static let hardReasons: [(key: String, label: String)] = [
    ("would_not_attend_with_again", "Would not attend with again"),
    ("concerning_behavior", "Concerning behavior"),
    ("made_me_uncomfortable", "Made me uncomfortable"),
  ]
  static let softReasonKey = "just_didnt_like_them"
  static let softReasonLabel = "I just didn't like them"

  var body: some View {
    NavigationStack {
      Group {
        if !loaded {
          ProgressView().controlSize(.large)
        } else if let err = loadError {
          VStack(spacing: 12) {
            Text("Couldn't load feedback")
              .font(.headline)
            Text(err)
              .font(.footnote)
              .foregroundStyle(.secondary)
              .multilineTextAlignment(.center)
              .padding(.horizontal, 24)
            Button("Close") { dismiss() }
              .buttonStyle(.borderedProminent)
          }
        } else if targets.isEmpty {
          VStack(spacing: 12) {
            Text("No fellow Attendees to rate.")
              .font(.headline)
              .foregroundStyle(.secondary)
            Button("Close") { dismiss() }
              .buttonStyle(.borderedProminent)
          }
        } else {
          attendeesList
        }
      }
      .navigationTitle("How was the Event?")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          Button("Close") { dismiss() }
        }
        ToolbarItem(placement: .topBarTrailing) {
          if loaded && !targets.isEmpty {
            Button(action: { Task { await submit() } }) {
              if submitting {
                ProgressView().controlSize(.small)
              } else {
                Text("Submit")
                  .fontWeight(.semibold)
              }
            }
            .disabled(submitting || selections.isEmpty)
          }
        }
      }
      .sheet(
        item: Binding(
          get: { reasoningFor.map { ReasoningTarget(id: $0) } },
          set: { reasoningFor = $0?.id }
        )
      ) { target in
        reasonsSheet(for: target.id)
      }
    }
    .task { await load() }
    .alert(
      "Submit failed",
      isPresented: Binding(
        get: { submitError != nil },
        set: { if !$0 { submitError = nil } }),
      presenting: submitError
    ) { _ in
      Button("OK") { submitError = nil }
    } message: { msg in
      Text(msg)
    }
  }

  private struct ReasoningTarget: Identifiable { let id: String }

  private var attendeesList: some View {
    List(targets) { target in
      VStack(alignment: .leading, spacing: 10) {
        Text(target.displayName.isEmpty ? "Attendee" : target.displayName)
          .font(.headline)
        ratingRow(for: target)
        if let sel = selections[target.id], sel.signal == "down" {
          reasonsBadge(for: target.id, reasons: sel.reasons)
        }
      }
      .padding(.vertical, 4)
    }
    .listStyle(.insetGrouped)
  }

  private func ratingRow(for target: EventsAPI.FeedbackTarget) -> some View {
    let current = selections[target.id]
    return HStack(spacing: 12) {
      pill(
        symbol: "hand.thumbsup.fill",
        label: "Good",
        active: current?.signal == "up",
        tint: .green
      ) {
        selections[target.id] = .up
      }
      pill(
        symbol: "hand.thumbsdown.fill",
        label: "Bad",
        active: current?.signal == "down",
        tint: .red
      ) {
        // First tap on 👎 opens the reasons sheet. The selection is
        // installed with whatever reasons the user already had.
        let preserved = current?.signal == "down" ? current!.reasons : []
        selections[target.id] = .down(reasons: preserved)
        reasoningFor = target.id
      }
      pill(
        symbol: "minus",
        label: "Skip",
        active: current?.signal == "skip",
        tint: .gray
      ) {
        selections[target.id] = .skip
      }
    }
  }

  private func pill(
    symbol: String, label: String, active: Bool, tint: Color, action: @escaping () -> Void
  ) -> some View {
    Button(action: action) {
      HStack(spacing: 6) {
        Image(systemName: symbol)
        Text(label)
      }
      .font(.subheadline.weight(.medium))
      .padding(.vertical, 8)
      .padding(.horizontal, 12)
      .frame(maxWidth: .infinity)
      .background(active ? tint.opacity(0.18) : Color.secondary.opacity(0.10))
      .foregroundStyle(active ? tint : .primary)
      .overlay(
        Capsule()
          .strokeBorder(active ? tint.opacity(0.6) : Color.clear, lineWidth: 1)
      )
      .clipShape(Capsule())
    }
    .buttonStyle(.plain)
  }

  private func reasonsBadge(for targetID: String, reasons: Set<String>) -> some View {
    let count = reasons.count
    let label: String =
      count == 0
      ? "Add reasons (optional)"
      : "\(count) reason\(count == 1 ? "" : "s")"
    return Button(action: { reasoningFor = targetID }) {
      HStack(spacing: 4) {
        Image(systemName: "line.3.horizontal.decrease.circle")
        Text(label)
      }
      .font(.caption)
      .foregroundStyle(.secondary)
    }
    .buttonStyle(.plain)
  }

  private func reasonsSheet(for targetID: String) -> some View {
    let current = selections[targetID]?.reasons ?? []
    return NavigationStack {
      Form {
        Section(
          header: Text("What happened?"),
          footer: Text(
            "Hard reasons send an anonymous flag that affects this user's reputation. "
              + "“I just didn't like them” is captured for the recipient's bundled feedback "
              + "but doesn't move their score.")
        ) {
          ForEach(Self.hardReasons, id: \.key) { reason in
            reasonRow(targetID: targetID, key: reason.key, label: reason.label, current: current)
          }
          reasonRow(
            targetID: targetID,
            key: Self.softReasonKey,
            label: Self.softReasonLabel,
            current: current)
        }
      }
      .navigationTitle("Reasons")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .confirmationAction) {
          Button("Done") { reasoningFor = nil }
        }
      }
    }
    .presentationDetents([.medium])
  }

  private func reasonRow(targetID: String, key: String, label: String, current: Set<String>)
    -> some View
  {
    let on = current.contains(key)
    return Button(action: { toggleReason(targetID: targetID, key: key) }) {
      HStack {
        Text(label)
          .foregroundStyle(.primary)
        Spacer()
        if on {
          Image(systemName: "checkmark")
            .foregroundStyle(.tint)
        }
      }
    }
  }

  private func toggleReason(targetID: String, key: String) {
    var sel = selections[targetID] ?? .down()
    if sel.reasons.contains(key) {
      sel.reasons.remove(key)
    } else {
      sel.reasons.insert(key)
    }
    sel.signal = "down"
    selections[targetID] = sel
  }

  // MARK: load + submit

  private func load() async {
    do {
      let resp = try await EventsAPI.fetchFeedback(eventID: eventID, auth: auth)
      targets = resp.targets
      // Pre-fill from prior submissions. A 👍/skip → empty reasons;
      // 👎 → the persisted reason set (which may be empty).
      var pre: [String: Selection] = [:]
      for s in resp.submitted {
        switch s.signal {
        case "up": pre[s.targetUserID] = .up
        case "skip": pre[s.targetUserID] = .skip
        case "down": pre[s.targetUserID] = .down(reasons: Set(s.reasons))
        default: break
        }
      }
      selections = pre
      loaded = true
    } catch let err as EventsAPI.FeedbackError {
      loaded = true
      loadError = err.errorDescription
    } catch {
      loaded = true
      loadError = error.localizedDescription
    }
  }

  private func submit() async {
    submitting = true
    submitError = nil
    defer { submitting = false }

    let signals = selections.map { (targetID, sel) -> EventsAPI.FeedbackSignal in
      let reasons: [String]? =
        sel.signal == "down" && !sel.reasons.isEmpty
        ? Array(sel.reasons)
        : nil
      return EventsAPI.FeedbackSignal(
        targetUserID: targetID, signal: sel.signal, reasons: reasons)
    }
    if signals.isEmpty { return }

    do {
      try await EventsAPI.submitFeedback(eventID: eventID, signals: signals, auth: auth)
      dismiss()
    } catch let err as EventsAPI.FeedbackError {
      submitError = err.errorDescription
    } catch {
      submitError = error.localizedDescription
    }
  }
}
