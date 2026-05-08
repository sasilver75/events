import CoreLocation
import MapKit
import PhotosUI
import SwiftUI

// CreateEventSheet collects the input for an Event and posts it via
// EventsAPI.create. Two flavors per PRD §Event creation:
//   - Hosted (α): the creator is the accountable Host; no Tip threshold.
//   - Seeded (β): the creator is a Seeder; the Event isn't binding until
//     N strangers Commit (Tip threshold), and is auto-cancelled if the
//     Tip deadline passes without reaching it. Per ADR 0001, Seeders
//     cannot cancel — only the auto-cancel deadline applies.
//
// Validation mirrors the server (issues #29, #32):
//   - title required
//   - start_time ∈ [now, now + 72hr]
//   - end_time > start_time
//   - cap, when set, ≥ 1
//   - β: tip_threshold ≥ 2; cap (if set) ≥ tip_threshold;
//        tip_deadline ∈ (now, start_time - 15min)
struct CreateEventSheet: View {
  let initialCenter: CLLocationCoordinate2D
  let onCreated: () -> Void

  @Environment(AuthModel.self) private var auth
  @Environment(\.dismiss) private var dismiss

  enum EventKind: String, CaseIterable, Identifiable {
    case hosted = "Hosted"
    case seeded = "Seeded"
    var id: String { rawValue }
  }

  @State private var title: String = ""
  @State private var description: String = ""
  @State private var category: EventCategory = .social
  @State private var startTime: Date = Date().addingTimeInterval(60 * 60)
  @State private var endTime: Date = Date().addingTimeInterval(2 * 60 * 60)
  @State private var pinCoordinate: CLLocationCoordinate2D
  @State private var capEnabled: Bool = true
  @State private var capValue: Int = 8
  @State private var visibilityFuzzed: Bool = true
  @State private var kind: EventKind = .hosted
  @State private var tipThreshold: Int = 4
  @State private var tipDeadline: Date = Date().addingTimeInterval(0)

  @State private var submitting: Bool = false
  @State private var submitError: String?

  // Banner picker state. The picker yields a PhotosPickerItem; we
  // resolve it to JPEG Data via .loadTransferable for upload, and
  // separately to a UIImage for in-form preview. Upload itself is
  // deferred to submit() so the user can add/remove freely.
  @State private var bannerPickerItem: PhotosPickerItem?
  @State private var bannerPreview: UIImage?
  @State private var bannerJPEG: Data?

  @State private var pinHint: String?
  @State private var recenterTrigger: Int = 0
  private let geocoder: GeocodingService = AppleGeocodingService()

  init(initialCenter: CLLocationCoordinate2D, onCreated: @escaping () -> Void) {
    self.initialCenter = initialCenter
    self.onCreated = onCreated
    self._pinCoordinate = State(initialValue: initialCenter)
    // Default the Tip deadline to halfway between now and start_time - 15min,
    // so it sits comfortably inside the (now, start_time - 15min] window for
    // any reasonable starting offset (PRD §Event creation gives "start - 1hr"
    // as the spec default; halving keeps the default robust when start_time
    // is close).
    let initialStart = Date().addingTimeInterval(60 * 60)
    let upper = initialStart.addingTimeInterval(-15 * 60)
    let now = Date()
    self._tipDeadline = State(
      initialValue: now.addingTimeInterval(upper.timeIntervalSince(now) / 2))
  }

  var body: some View {
    NavigationStack {
      Form {
        Section("Kind") {
          Picker("Event kind", selection: $kind) {
            ForEach(EventKind.allCases) { k in
              Text(k.rawValue).tag(k)
            }
          }
          .pickerStyle(.segmented)
          .accessibilityIdentifier("create.kind")
          Text(
            kind == .hosted
              ? "You're hosting. Your reputation is on the line."
              : "Open to the crowd. Auto-cancels if it doesn't Tip by the deadline."
          )
          .font(.caption)
          .foregroundStyle(.secondary)
        }

        Section("Banner") {
          if let preview = bannerPreview {
            Image(uiImage: preview)
              .resizable()
              .scaledToFill()
              .frame(maxWidth: .infinity)
              .frame(height: 160)
              .clipShape(RoundedRectangle(cornerRadius: 12))
              .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 8, trailing: 8))
              .accessibilityIdentifier("create.bannerPreview")
            Button("Remove banner", role: .destructive) {
              bannerPickerItem = nil
              bannerPreview = nil
              bannerJPEG = nil
            }
            .accessibilityIdentifier("create.bannerRemove")
          } else {
            PhotosPicker(
              selection: $bannerPickerItem,
              matching: .images,
              photoLibrary: .shared()
            ) {
              Label("Add banner image", systemImage: "photo.badge.plus")
            }
            .accessibilityIdentifier("create.bannerPicker")
            Text("Optional. Up to 2 MB, JPEG/PNG/HEIC/WebP.")
              .font(.caption)
              .foregroundStyle(.secondary)
          }
        }

        Section("Event") {
          TextField("Title", text: $title)
            .accessibilityIdentifier("create.title")
          TextField("Description", text: $description, axis: .vertical)
            .lineLimit(3...6)
            .accessibilityIdentifier("create.description")
          Text("Markdown supported")
            .font(.caption)
            .foregroundStyle(.secondary)
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
            // Re-clamp the Tip deadline so it stays inside the valid window.
            let upper = new.addingTimeInterval(-15 * 60)
            if tipDeadline >= upper { tipDeadline = upper.addingTimeInterval(-1) }
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
          LocationSearchField(
            region: searchRegion,
            geocoder: geocoder
          ) { result in
            pinCoordinate = result.coordinate
            pinHint =
              result.subtitle.isEmpty ? result.title : "\(result.title) · \(result.subtitle)"
            recenterTrigger &+= 1
          }
          .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 0, trailing: 8))
          LocationPickerMap(
            styleURL: Bundle.main.mapStyleLightURL,
            initialCenter: initialCenter,
            coordinate: $pinCoordinate,
            recenterTrigger: recenterTrigger
          )
          .frame(height: 220)
          .clipShape(RoundedRectangle(cornerRadius: 12))
          .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 8, trailing: 8))
          if let hint = pinHint, !hint.isEmpty {
            Text(hint)
              .font(.caption)
              .foregroundStyle(.secondary)
              .accessibilityIdentifier("create.locationHint")
          }
          Text(formatCoord(pinCoordinate))
            .font(.caption.monospaced())
            .foregroundStyle(.secondary)
          Toggle("Hide exact location until people Commit", isOn: $visibilityFuzzed)
            .accessibilityIdentifier("create.fuzzed")
        }
        .task(id: pinCoordinateKey) { await refreshPinHint(for: pinCoordinate) }

        Section("Cap") {
          Toggle("Cap attendance", isOn: $capEnabled)
            .accessibilityIdentifier("create.capEnabled")
          if capEnabled {
            Stepper("\(capValue) attendees", value: $capValue, in: 1...50)
              .accessibilityIdentifier("create.cap")
          }
        }

        if kind == .seeded {
          Section("Tip") {
            Stepper("Tip at \(tipThreshold) Commits", value: $tipThreshold, in: 2...50)
              .accessibilityIdentifier("create.tipThreshold")
            DatePicker(
              "Deadline",
              selection: $tipDeadline,
              in: Date()...startTime.addingTimeInterval(-15 * 60)
            )
            .accessibilityIdentifier("create.tipDeadline")
            Text("If \(tipThreshold) Commits don't land by the deadline, the Event auto-cancels.")
              .font(.caption)
              .foregroundStyle(.secondary)
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
      .onChange(of: bannerPickerItem) { _, item in
        Task { await loadBanner(from: item) }
      }
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
    let titleOK = !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    let timeOK = endTime > startTime
    guard titleOK && timeOK else { return false }
    if kind == .seeded {
      guard tipThreshold >= 2 else { return false }
      if capEnabled && capValue < tipThreshold { return false }
      let now = Date()
      let upper = startTime.addingTimeInterval(-15 * 60)
      if tipDeadline <= now || tipDeadline > upper { return false }
    }
    return true
  }

  private func submit() async {
    submitting = true
    submitError = nil
    defer { submitting = false }

    // Upload banner first, then POST /events. If the POST fails after
    // the upload lands, the object is orphaned in Storage — Wave-2 GC
    // is tracked in server/README.md.
    var bannerPath: String?
    if let jpeg = bannerJPEG {
      guard let userID = auth.userID else {
        submitError = "Not signed in"
        return
      }
      do {
        bannerPath = try await BannerStorage.upload(jpegData: jpeg, userID: userID)
      } catch {
        submitError = "Banner upload failed: \(error.localizedDescription)"
        return
      }
    }

    let (thr, dl): (Int?, Date?) =
      kind == .seeded ? (tipThreshold, tipDeadline) : (nil, nil)
    let input = EventsAPI.CreateInput(
      title: title.trimmingCharacters(in: .whitespacesAndNewlines),
      description: description,
      category: category.rawValue,
      startTime: startTime,
      endTime: endTime,
      lat: pinCoordinate.latitude,
      lon: pinCoordinate.longitude,
      cap: capEnabled ? capValue : nil,
      locationVisibility: visibilityFuzzed ? "fuzzed" : "public",
      tipThreshold: thr,
      tipDeadline: dl,
      bannerPath: bannerPath)
    do {
      try await EventsAPI.create(input, auth: auth)
      onCreated()
      dismiss()
    } catch {
      submitError = error.localizedDescription
    }
  }

  private func loadBanner(from item: PhotosPickerItem?) async {
    guard let item else { return }
    do {
      guard let data = try await item.loadTransferable(type: Data.self) else { return }
      guard let raw = UIImage(data: data) else {
        submitError = "Couldn't read image"
        return
      }
      // Downscale + re-encode to JPEG to (a) normalize content-type
      // (HEIC from the camera roll otherwise mismatches what we
      // declare) and (b) stay under the 2 MiB bucket cap. Camera-roll
      // photos are routinely 12+ MP / 4-6 MB and the bucket rejects
      // them with 413.
      let resized = downscale(raw, maxDimension: 1600)
      let jpeg = compressUnderCap(resized, capBytes: 2 * 1024 * 1024)
      bannerPreview = resized
      bannerJPEG = jpeg
    } catch {
      submitError = "Couldn't read image: \(error.localizedDescription)"
    }
  }

  private func downscale(_ image: UIImage, maxDimension: CGFloat) -> UIImage {
    let size = image.size
    let longest = max(size.width, size.height)
    guard longest > maxDimension else { return image }
    let scale = maxDimension / longest
    let target = CGSize(width: size.width * scale, height: size.height * scale)
    let format = UIGraphicsImageRendererFormat.default()
    format.scale = 1
    return UIGraphicsImageRenderer(size: target, format: format).image { _ in
      image.draw(in: CGRect(origin: .zero, size: target))
    }
  }

  private func compressUnderCap(_ image: UIImage, capBytes: Int) -> Data {
    var quality: CGFloat = 0.85
    var data = image.jpegData(compressionQuality: quality) ?? Data()
    while data.count > capBytes && quality > 0.3 {
      quality -= 0.1
      data = image.jpegData(compressionQuality: quality) ?? data
    }
    return data
  }

  private func formatCoord(_ c: CLLocationCoordinate2D) -> String {
    String(format: "Pin: %.5f, %.5f", c.latitude, c.longitude)
  }

  private var pinCoordinateKey: String {
    "\(pinCoordinate.latitude),\(pinCoordinate.longitude)"
  }

  private var searchRegion: MKCoordinateRegion {
    MKCoordinateRegion(
      center: pinCoordinate,
      span: MKCoordinateSpan(latitudeDelta: 0.4, longitudeDelta: 0.4))
  }

  private func refreshPinHint(for coord: CLLocationCoordinate2D) async {
    let resolved = await geocoder.reverseGeocode(coord)
    pinHint = resolved
  }
}
