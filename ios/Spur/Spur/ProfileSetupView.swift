import PhotosUI
import SwiftUI
import UIKit

// ProfileSetupView is the post-OTP signup flow that completes the
// public.users row required by ADR 0025: selfie → DOB → ToS scroll-and-
// accept → handle + display_name → submit. Submit calls
// POST /users/me/profile, uploads the selfie to the avatars Storage bucket,
// then calls POST /users/me/avatar. On success it flips ProfileModel to
// .complete so RootView swaps to the map.
//
// Liveness (the Vision-based blink/head-turn challenge from ADR 0007) is
// out of scope here per #88; selfie capture is still-image only and the
// sibling ticket #89 wires liveness in.
struct ProfileSetupView: View {
  @Environment(AuthModel.self) private var auth
  @Environment(ProfileModel.self) private var profile

  enum Step: Int, CaseIterable {
    case selfie, dob, tos, identity
  }

  @State private var step: Step = .selfie
  @State private var selfieImage: UIImage?
  @State private var dob: Date = Self.defaultDOB
  @State private var tosVersion: String?
  @State private var tosContent: String = ""
  @State private var tosAccepted: Bool = false
  @State private var handle: String = ""
  @State private var displayName: String = ""
  @State private var submitting: Bool = false
  @State private var submitError: String?

  // The DOB picker defaults to a long-ago year so the user has to scroll
  // forward rather than past the 18-year boundary — and so a user who
  // taps "Next" without changing the value still passes the 18+ check.
  private static let defaultDOB: Date = {
    Calendar.current.date(byAdding: .year, value: -25, to: Date()) ?? Date()
  }()

  var body: some View {
    NavigationStack {
      VStack(spacing: 0) {
        progressBar
        Divider()
        Group {
          switch step {
          case .selfie: SelfieStep(image: $selfieImage, onNext: { step = .dob })
          case .dob: DOBStep(dob: $dob, onNext: { step = .tos })
          case .tos:
            ToSStep(
              version: $tosVersion,
              content: $tosContent,
              accepted: $tosAccepted,
              onNext: { step = .identity }
            )
          case .identity:
            IdentityStep(
              handle: $handle,
              displayName: $displayName,
              submitting: submitting,
              submitError: submitError,
              onSubmit: { Task { await submit() } }
            )
          }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
      }
      .navigationTitle(stepTitle)
      .navigationBarTitleDisplayMode(.inline)
    }
  }

  private var stepTitle: String {
    switch step {
    case .selfie: return "Take a selfie"
    case .dob: return "Date of birth"
    case .tos: return "Terms of Service"
    case .identity: return "Pick a handle"
    }
  }

  private var progressBar: some View {
    let total = Step.allCases.count
    return ProgressView(value: Double(step.rawValue + 1), total: Double(total))
      .padding(.horizontal, 16)
      .padding(.vertical, 8)
  }

  private func submit() async {
    submitError = nil
    submitting = true
    defer { submitting = false }

    guard let selfie = selfieImage else {
      submitError = "Selfie missing — go back and capture one."
      return
    }
    guard let userID = auth.userID else {
      submitError = "Sign-in lost — please restart."
      return
    }
    guard let version = tosVersion, tosAccepted else {
      submitError = "Accept the Terms of Service first."
      return
    }

    let dobString = Self.dobFormatter.string(from: dob)
    let trimmedHandle = handle.trimmingCharacters(in: .whitespaces)
    let trimmedDisplay = displayName.trimmingCharacters(in: .whitespaces)

    do {
      // 1) Upsert profile first — this is what creates the public.users row.
      //    Avatar upload that follows is gated by RequireProfile on the
      //    server, so this order is load-bearing.
      _ = try await UsersAPI.upsertProfile(
        UsersAPI.ProfileInput(
          handle: trimmedHandle.lowercased(),
          handleDisplay: trimmedHandle,
          displayName: trimmedDisplay,
          dob: dobString,
          tosVersion: version
        ),
        auth: auth
      )

      // 2) Upload avatar JPEG to the public-read avatars bucket under the
      //    user's prefix, then point users.avatar_path at the path.
      let path = try await AvatarStorage.upload(image: selfie, userID: userID)
      try await UsersAPI.setAvatarPath(path, auth: auth)

      profile.markComplete()
    } catch let err as UsersAPI.APIError {
      submitError = err.errorDescription
      // If the handle came back taken, drop back to the identity step
      // (we're already there) so the user can try a new one.
      if err == .handleTaken {
        step = .identity
      }
      if err == .tosVersionMismatch {
        // The ToS revised between fetch and submit. Re-fetch and re-prompt.
        tosAccepted = false
        step = .tos
      }
    } catch {
      submitError = error.localizedDescription
    }
  }

  private static let dobFormatter: DateFormatter = {
    let f = DateFormatter()
    f.dateFormat = "yyyy-MM-dd"
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = TimeZone(secondsFromGMT: 0)
    return f
  }()
}

// MARK: — Selfie

private struct SelfieStep: View {
  @Binding var image: UIImage?
  let onNext: () -> Void

  @State private var sheetOpen = false
  @State private var pickerItem: PhotosPickerItem?

  var body: some View {
    VStack(spacing: 24) {
      Spacer()
      preview
      Spacer()

      // The simulator has no camera; PhotosPicker against the sim's
      // photo library lets the rest of the flow run end-to-end without
      // device hardware. On device we use UIImagePickerController with
      // sourceType=.camera so the user gets a real selfie capture.
      #if targetEnvironment(simulator)
        PhotosPicker(selection: $pickerItem, matching: .images) {
          buttonLabel(image == nil ? "Pick a photo" : "Pick a different photo")
        }
        .onChange(of: pickerItem) { _, newItem in
          guard let newItem else { return }
          Task { await loadFromPicker(newItem) }
        }
      #else
        Button {
          sheetOpen = true
        } label: {
          buttonLabel(image == nil ? "Take selfie" : "Retake selfie")
        }
        .sheet(isPresented: $sheetOpen) {
          CameraPicker(image: $image)
        }
      #endif

      Button("Next", action: onNext)
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .disabled(image == nil)
        .padding(.bottom, 24)
    }
    .padding(.horizontal, 24)
  }

  @ViewBuilder
  private var preview: some View {
    if let image {
      Image(uiImage: image)
        .resizable()
        .scaledToFill()
        .frame(width: 200, height: 200)
        .clipShape(Circle())
        .overlay(Circle().stroke(Color.secondary.opacity(0.3), lineWidth: 1))
    } else {
      ZStack {
        Circle().fill(Color.secondary.opacity(0.15))
        Image(systemName: "person.crop.circle.fill")
          .font(.system(size: 80))
          .foregroundStyle(.secondary)
      }
      .frame(width: 200, height: 200)
    }
  }

  private func buttonLabel(_ text: String) -> some View {
    Text(text).font(.body.weight(.medium))
      .frame(maxWidth: .infinity)
      .padding(.vertical, 12)
      .background(Color.secondary.opacity(0.18), in: RoundedRectangle(cornerRadius: 10))
  }

  private func loadFromPicker(_ item: PhotosPickerItem) async {
    if let data = try? await item.loadTransferable(type: Data.self),
      let ui = UIImage(data: data)
    {
      image = ui
    }
  }
}

#if !targetEnvironment(simulator)
  private struct CameraPicker: UIViewControllerRepresentable {
    @Binding var image: UIImage?
    @Environment(\.dismiss) private var dismiss

    func makeUIViewController(context: Context) -> UIImagePickerController {
      let p = UIImagePickerController()
      p.sourceType = .camera
      p.cameraDevice = .front
      p.cameraCaptureMode = .photo
      p.allowsEditing = false
      p.delegate = context.coordinator
      return p
    }

    func updateUIViewController(_ uiViewController: UIImagePickerController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate,
      UINavigationControllerDelegate
    {
      let parent: CameraPicker
      init(_ parent: CameraPicker) { self.parent = parent }

      func imagePickerController(
        _ picker: UIImagePickerController,
        didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]
      ) {
        if let ui = info[.originalImage] as? UIImage {
          parent.image = ui
        }
        parent.dismiss()
      }

      func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        parent.dismiss()
      }
    }
  }
#endif

// MARK: — DOB

private struct DOBStep: View {
  @Binding var dob: Date
  let onNext: () -> Void

  private var eighteenYearsAgo: Date {
    Calendar.current.date(byAdding: .year, value: -18, to: Date()) ?? Date()
  }

  private var ageOK: Bool { dob <= eighteenYearsAgo }

  var body: some View {
    VStack(spacing: 24) {
      Text("You must be at least 18 to use Spur.")
        .font(.subheadline)
        .foregroundStyle(.secondary)
        .multilineTextAlignment(.center)
        .padding(.top, 24)

      DatePicker(
        "Date of birth",
        selection: $dob,
        in: ...eighteenYearsAgo,
        displayedComponents: .date
      )
      .datePickerStyle(.wheel)
      .labelsHidden()
      .padding(.horizontal, 24)

      Spacer()

      Button("Next", action: onNext)
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .disabled(!ageOK)
        .padding(.bottom, 24)
    }
    .padding(.horizontal, 24)
  }
}

// MARK: — ToS

private struct ToSStep: View {
  @Binding var version: String?
  @Binding var content: String
  @Binding var accepted: Bool
  let onNext: () -> Void

  @State private var loadError: String?
  @State private var scrolledToBottom: Bool = false

  var body: some View {
    VStack(spacing: 0) {
      if let loadError {
        ContentUnavailableView(
          "Couldn't load Terms",
          systemImage: "exclamationmark.triangle",
          description: Text(loadError))
      } else if content.isEmpty {
        ProgressView().controlSize(.large)
          .frame(maxWidth: .infinity, maxHeight: .infinity)
      } else {
        ScrollView {
          // SwiftUI's AttributedString markdown init handles inline syntax
          // (bold, italic, links) but renders block-level elements like
          // `## Headings` as literal `##` text. Split the content into
          // paragraph blocks and dispatch headings to a styled Text view —
          // simpler than bringing in a third-party markdown renderer for
          // a single screen with a known set of block types.
          VStack(alignment: .leading, spacing: 12) {
            ForEach(Array(markdownBlocks.enumerated()), id: \.offset) { _, block in
              ToSBlockView(block: block)
            }
          }
          .frame(maxWidth: .infinity, alignment: .leading)
          .padding(16)
        }
        // onScrollGeometryChange (iOS 18+) gives us live container/content
        // sizes plus the scroll offset on each tick. We declare "at bottom"
        // when contentSize - offset - containerSize ≤ 50pt, which is the
        // standard hysteresis for sub-pixel jitter and short-content cases
        // (where the content fits in one viewport — atBottom is true from
        // the start, which is the right behavior).
        //
        // Sentinel-on-onAppear at the bottom of the ScrollView's content
        // doesn't work: SwiftUI lays out the entire ScrollView content at
        // initial render, so .onAppear fires before the user has scrolled.
        .onScrollGeometryChange(for: Bool.self) { geometry in
          let remaining =
            geometry.contentSize.height - geometry.contentOffset.y - geometry.containerSize.height
          return remaining <= 50
        } action: { _, atBottom in
          if atBottom { scrolledToBottom = true }
        }
      }

      Divider()
      VStack(spacing: 8) {
        if !scrolledToBottom && loadError == nil && !content.isEmpty {
          Text("Scroll to the end to accept.")
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        Button {
          accepted = true
          onNext()
        } label: {
          Text("I accept")
            .font(.body.weight(.semibold))
            .frame(maxWidth: .infinity)
            .padding(.vertical, 12)
        }
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .disabled(!scrolledToBottom)
      }
      .padding(16)
    }
    .task {
      await loadToS()
    }
  }

  // markdownBlocks splits the ToS content on blank lines and tags each
  // block as h1/h2/h3 (leading `#`/`##`/`###`) or paragraph. Inline syntax
  // (bold/italic) inside paragraphs still goes through AttributedString in
  // ToSBlockView, so links and emphasis render correctly.
  private var markdownBlocks: [ToSBlock] {
    content
      .components(separatedBy: "\n\n")
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }
      .map(ToSBlock.parse)
  }

  private func loadToS() async {
    guard content.isEmpty else { return }
    do {
      let tos = try await UsersAPI.fetchToS()
      version = tos.version
      content = tos.content
    } catch {
      loadError = error.localizedDescription
    }
  }

}

// MARK: — Identity (handle + display_name)

// ToSBlock is the discriminated representation of a single markdown block
// inside the ToS content. Three block types are enough for tos-v1.md: h1,
// h2, h3 (used as section markers) and a paragraph fallback.
private enum ToSBlock {
  case h1(String)
  case h2(String)
  case h3(String)
  case paragraph(String)

  nonisolated static func parse(_ raw: String) -> ToSBlock {
    if raw.hasPrefix("### ") {
      return .h3(String(raw.dropFirst(4)))
    }
    if raw.hasPrefix("## ") {
      return .h2(String(raw.dropFirst(3)))
    }
    if raw.hasPrefix("# ") {
      return .h1(String(raw.dropFirst(2)))
    }
    return .paragraph(raw)
  }
}

private struct ToSBlockView: View {
  let block: ToSBlock

  var body: some View {
    switch block {
    case .h1(let s):
      Text(inline(s))
        .font(.title2.weight(.bold))
        .padding(.top, 8)
    case .h2(let s):
      Text(inline(s))
        .font(.headline)
        .padding(.top, 6)
    case .h3(let s):
      Text(inline(s))
        .font(.subheadline.weight(.semibold))
        .padding(.top, 4)
    case .paragraph(let s):
      Text(inline(s))
        .font(.body)
        .fixedSize(horizontal: false, vertical: true)
    }
  }

  // inline runs the block text through AttributedString's markdown init so
  // **bold**, *italic*, and links render. Block-level syntax has already
  // been stripped above, so this only sees inline runs.
  private func inline(_ s: String) -> AttributedString {
    (try? AttributedString(
      markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
    )) ?? AttributedString(s)
  }
}

private struct IdentityStep: View {
  @Binding var handle: String
  @Binding var displayName: String
  let submitting: Bool
  let submitError: String?
  let onSubmit: () -> Void

  @State private var availability: UsersAPI.HandleAvailability?
  @State private var probing: Bool = false
  @State private var probeTask: Task<Void, Never>?

  // Mirror of server-side regex in users.go and the CHECK in migration 0016.
  private static let handlePattern = #"^[a-z0-9_]{3,20}$"#

  private var canSubmit: Bool {
    !submitting
      && !displayName.trimmingCharacters(in: .whitespaces).isEmpty
      && availability == .available
  }

  var body: some View {
    Form {
      Section {
        TextField("SamSilver", text: $handle)
          .textInputAutocapitalization(.never)
          .autocorrectionDisabled()
          .keyboardType(.asciiCapable)
          .onChange(of: handle) { _, new in
            // Strip disallowed characters but preserve the user's casing —
            // that's what we'll send as handle_display. The canonical
            // (lowercased) form is derived at submit time.
            let filtered = String(new.filter(isHandleChar))
            if filtered != new { handle = filtered }
            scheduleProbe()
          }
          .accessibilityIdentifier("identity.handle")
        availabilityRow
      } header: {
        Text("Handle")
      } footer: {
        Text(
          "3–20 characters. Letters, digits, underscore. Mixed case OK — friends can find you in any casing."
        )
      }

      Section {
        TextField("Sam Silver", text: $displayName)
          .accessibilityIdentifier("identity.displayName")
      } header: {
        Text("Display name")
      } footer: {
        Text("How your name shows up to others. Doesn't have to be unique.")
      }

      if let submitError {
        Section {
          Text(submitError).foregroundStyle(.red).font(.footnote)
        }
      }

      Section {
        Button {
          onSubmit()
        } label: {
          HStack {
            if submitting { ProgressView() }
            Text(submitting ? "Submitting…" : "Finish signup")
          }
          .frame(maxWidth: .infinity)
        }
        .disabled(!canSubmit)
      }
    }
  }

  @ViewBuilder
  private var availabilityRow: some View {
    if probing {
      HStack {
        ProgressView().controlSize(.mini)
        Text("Checking…").font(.caption)
      }
      .foregroundStyle(.secondary)
    } else if let a = availability {
      switch a {
      case .available:
        Label("Available", systemImage: "checkmark.circle.fill")
          .foregroundStyle(.green).font(.caption)
      case .taken:
        Label("Taken", systemImage: "xmark.circle.fill")
          .foregroundStyle(.red).font(.caption)
      case .invalidFormat:
        Label("Invalid format", systemImage: "exclamationmark.triangle.fill")
          .foregroundStyle(.orange).font(.caption)
      }
    }
  }

  private func isHandleChar(_ c: Character) -> Bool {
    c.isASCII && (c.isLetter || c.isNumber || c == "_")
  }

  private func scheduleProbe() {
    probeTask?.cancel()
    // Validate + probe against the lowercased canonical form. The displayed
    // value preserves the user's casing for handle_display.
    let candidate = handle.lowercased()
    if candidate.count < 3 || candidate.count > 20 {
      availability = candidate.isEmpty ? nil : .invalidFormat
      probing = false
      return
    }
    if candidate.range(of: Self.handlePattern, options: .regularExpression) == nil {
      availability = .invalidFormat
      probing = false
      return
    }
    probing = true
    probeTask = Task {
      try? await Task.sleep(nanoseconds: 250_000_000)
      if Task.isCancelled { return }
      do {
        let a = try await UsersAPI.probeHandle(candidate)
        if Task.isCancelled { return }
        availability = a
      } catch {
        if !Task.isCancelled { availability = nil }
      }
      probing = false
    }
  }
}
