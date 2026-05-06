import CoreLocation
import SwiftUI

// CreateEventSheet collects the input for an α-Event (Hosted) and posts it
// via EventsAPI.create. Validation mirrors the server (PRD US 13 / issue
// #29) so the user gets feedback before the round-trip:
//   - title required
//   - start_time ∈ [now, now + 72hr]
//   - end_time > start_time
//   - cap, when set, ≥ 1
//
// β-Events / Tip threshold (#32) and rep-/friends-gating (#38, future) are
// not surfaced here — this sheet is α-only.
struct CreateEventSheet: View {
  let initialCenter: CLLocationCoordinate2D
  let onCreated: () -> Void

  @Environment(AuthModel.self) private var auth
  @Environment(\.dismiss) private var dismiss

  @State private var title: String = ""
  @State private var description: String = ""
  @State private var category: EventCategory = .social
  @State private var startTime: Date = Date().addingTimeInterval(60 * 60)
  @State private var endTime: Date = Date().addingTimeInterval(2 * 60 * 60)
  @State private var pinCoordinate: CLLocationCoordinate2D
  @State private var capEnabled: Bool = true
  @State private var capValue: Int = 8
  @State private var visibilityFuzzed: Bool = true

  @State private var submitting: Bool = false
  @State private var submitError: String?

  init(initialCenter: CLLocationCoordinate2D, onCreated: @escaping () -> Void) {
    self.initialCenter = initialCenter
    self.onCreated = onCreated
    self._pinCoordinate = State(initialValue: initialCenter)
  }

  var body: some View {
    NavigationStack {
      Form {
        Section("Event") {
          TextField("Title", text: $title)
            .accessibilityIdentifier("create.title")
          TextField("Description", text: $description, axis: .vertical)
            .lineLimit(3...6)
            .accessibilityIdentifier("create.description")
          Picker("Category", selection: $category) {
            ForEach(EventCategory.allCases, id: \.self) { c in
              Text(c.rawValue).tag(c)
            }
          }
          .accessibilityIdentifier("create.category")
        }

        Section("When") {
          DatePicker(
            "Start",
            selection: $startTime,
            in: Date()...Date().addingTimeInterval(72 * 60 * 60)
          )
          .onChange(of: startTime) { _, new in
            if endTime <= new { endTime = new.addingTimeInterval(60 * 60) }
          }
          .accessibilityIdentifier("create.startTime")
          DatePicker(
            "End",
            selection: $endTime,
            in: startTime.addingTimeInterval(15 * 60)...startTime.addingTimeInterval(12 * 60 * 60)
          )
          .accessibilityIdentifier("create.endTime")
        }

        Section("Where") {
          LocationPickerMap(
            styleURL: Bundle.main.mapStyleLightURL,
            initialCenter: initialCenter,
            coordinate: $pinCoordinate
          )
          .frame(height: 220)
          .clipShape(RoundedRectangle(cornerRadius: 12))
          .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 8, trailing: 8))
          Text(formatCoord(pinCoordinate))
            .font(.caption.monospaced())
            .foregroundStyle(.secondary)
          Toggle("Hide exact location until people Commit", isOn: $visibilityFuzzed)
            .accessibilityIdentifier("create.fuzzed")
        }

        Section("Cap") {
          Toggle("Cap attendance", isOn: $capEnabled)
            .accessibilityIdentifier("create.capEnabled")
          if capEnabled {
            Stepper("\(capValue) attendees", value: $capValue, in: 1...50)
              .accessibilityIdentifier("create.cap")
          }
        }

        if let err = submitError {
          Section {
            Text(err).foregroundStyle(.red).font(.footnote)
          }
        }
      }
      .navigationTitle("New Event")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) {
          Button("Cancel") { dismiss() }
            .disabled(submitting)
        }
        ToolbarItem(placement: .confirmationAction) {
          Button(submitting ? "Creating…" : "Create") {
            Task { await submit() }
          }
          .disabled(submitting || !canSubmit)
          .accessibilityIdentifier("create.submit")
        }
      }
    }
  }

  private var canSubmit: Bool {
    !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      && endTime > startTime
  }

  private func submit() async {
    submitting = true
    submitError = nil
    defer { submitting = false }

    let input = EventsAPI.CreateInput(
      title: title.trimmingCharacters(in: .whitespacesAndNewlines),
      description: description,
      category: category.rawValue,
      startTime: startTime,
      endTime: endTime,
      lat: pinCoordinate.latitude,
      lon: pinCoordinate.longitude,
      cap: capEnabled ? capValue : nil,
      locationVisibility: visibilityFuzzed ? "fuzzed" : "public")
    do {
      try await EventsAPI.create(input, auth: auth)
      onCreated()
      dismiss()
    } catch {
      submitError = error.localizedDescription
    }
  }

  private func formatCoord(_ c: CLLocationCoordinate2D) -> String {
    String(format: "Pin: %.5f, %.5f", c.latitude, c.longitude)
  }
}
