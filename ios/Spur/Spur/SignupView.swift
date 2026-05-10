import PhotosUI
import SwiftUI
import UIKit

// SignupView walks the user from a fresh JWT through profile completion
// (handle, display name, DOB, ToS, avatar). Driven by AuthModel's signup
// completion state — the same view handles both fresh signups and the
// resume-into-signup case (ADR 0025) where the user already submitted
// profile data but hasn't uploaded an avatar.
//
// Step order follows the brief: selfie → DOB → ToS → handle. The avatar
// upload to Supabase Storage happens at submit time, not at capture time,
// so the user can retake before committing to upload bytes.
//
// Liveness (Vision blink/head-turn per ADR 0007) is split out into #89.
struct SignupView: View {
  @Environment(AuthModel.self) private var auth
  let initialStep: Step

  enum Step: Int, CaseIterable, Comparable {
    case selfie, dob, tos, handle

    static func < (lhs: Step, rhs: Step) -> Bool { lhs.rawValue < rhs.rawValue }

    var title: String {
      switch self {
      case .selfie: return "Add a photo"
      case .dob: return "Date of birth"
      case .tos: return "Terms"
      case .handle: return "Choose a handle"
      }
    }
  }

  @State private var step: Step
  @State private var selfieImage: UIImage?
  @State private var dob: Date = Calendar.current.date(byAdding: .year, value: -25, to: Date())!
  @State private var tos: UsersAPI.TOSDocument?
  @State private var tosLoadError: String?
  @State private var tosScrolledToEnd = false
  @State private var tosAccepted = false
  @State private var handle: String = ""
  @State private var displayName: String = ""
  @State private var handleAvailable: Bool? = nil
  @State private var handleProbeError: String?
  @State private var handleProbeTask: Task<Void, Never>?

  @State private var submitting = false
  @State private var submitError: String?

  init(initialStep: Step = .selfie) {
    self.initialStep = initialStep
    _step = State(initialValue: initialStep)
  }

  var body: some View {
    NavigationStack {
      Form {
        switch step {
        case .selfie: selfieSection
        case .dob: dobSection
        case .tos: tosSection
        case .handle: handleSection
        }

        if let submitError {
          Section {
            Text(submitError)
              .foregroundStyle(.red)
              .font(.footnote)
          }
        }
      }
      .navigationTitle(step.title)
      .toolbar {
        if step != .selfie {
          ToolbarItem(placement: .topBarLeading) {
            Button("Back") { goBack() }
              .accessibilityIdentifier("signup.back")
          }
        }
        ToolbarItem(placement: .topBarTrailing) {
          Button("Sign out") {
            Task { await auth.signOut() }
          }
          .foregroundStyle(.secondary)
        }
      }
      .task { await loadTosIfNeeded() }
    }
  }

  // MARK: Step 1 — selfie

  @ViewBuilder
  private var selfieSection: some View {
    Section {
      if let img = selfieImage {
        Image(uiImage: img)
          .resizable()
          .scaledToFill()
          .frame(width: 160, height: 160)
          .clipShape(Circle())
          .frame(maxWidth: .infinity, alignment: .center)
      }
      SelfiePickerButton(image: $selfieImage)
    } footer: {
      Text(
        "Other Spur users see this on your profile, attendee lists, and event pins. Pick a clear, well-lit shot of yourself."
      )
    }

    Section {
      Button("Continue") { advance(from: .selfie) }
        .disabled(selfieImage == nil)
        .accessibilityIdentifier("signup.continue")
    }
  }

  // MARK: Step 2 — DOB

  @ViewBuilder
  private var dobSection: some View {
    Section {
      DatePicker("Date of birth", selection: $dob, in: ...Date(), displayedComponents: .date)
        .datePickerStyle(.wheel)
        .accessibilityIdentifier("signup.dob")
    } footer: {
      Text("You must be at least 18 to use Spur. We never show your DOB to other users.")
    }

    Section {
      Button("Continue") { advance(from: .dob) }
        .disabled(!is18OrOlder(dob))
        .accessibilityIdentifier("signup.continue")
      if !is18OrOlder(dob) {
        Text("Must be at least 18 years old.")
          .foregroundStyle(.red)
          .font(.footnote)
      }
    }
  }

  // MARK: Step 3 — ToS

  @ViewBuilder
  private var tosSection: some View {
    Section {
      if let tos {
        ScrollViewReader { proxy in
          ScrollView {
            Text(tos.content)
              .font(.callout)
              .padding(.bottom, 8)
            // Sentinel anchor — scrolling it into view via onAppear
            // tells us the user actually reached the end.
            Color.clear
              .frame(height: 1)
              .id("end")
              .onAppear { tosScrolledToEnd = true }
          }
          .frame(maxHeight: 320)
          .onAppear { _ = proxy }  // keep proxy retained for SwiftUI lifecycle
        }
      } else if let tosLoadError {
        Text(tosLoadError).foregroundStyle(.red)
        Button("Retry") {
          Task { await loadTosIfNeeded(force: true) }
        }
      } else {
        ProgressView().frame(maxWidth: .infinity)
      }
    } header: {
      Text("Read and accept")
    }

    Section {
      Toggle("I accept the Terms of Service", isOn: $tosAccepted)
        .disabled(!tosScrolledToEnd)
        .accessibilityIdentifier("signup.tos.accept")
      Button("Continue") { advance(from: .tos) }
        .disabled(!tosAccepted)
        .accessibilityIdentifier("signup.continue")
    }
  }

  // MARK: Step 4 — handle + display name

  @ViewBuilder
  private var handleSection: some View {
    Section {
      TextField("Display name", text: $displayName)
        .textInputAutocapitalization(.words)
        .accessibilityIdentifier("signup.displayName")
    } header: {
      Text("Display name")
    } footer: {
      Text("Other people see this on your profile and on attendee lists.")
    }

    Section {
      TextField("handle", text: $handle)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .onChange(of: handle) { _, new in
          let cleaned = new.lowercased().filter { isHandleChar($0) }
          if cleaned != handle { handle = cleaned }
          scheduleHandleProbe()
        }
        .accessibilityIdentifier("signup.handle")
      handleStatusRow
    } header: {
      Text("Handle")
    } footer: {
      Text("3–20 lowercase letters, digits, or underscores. Friends can find you by handle.")
    }

    Section {
      Button(submitting ? "Creating profile…" : "Create profile") {
        Task { await submit() }
      }
      .disabled(!canSubmit || submitting)
      .accessibilityIdentifier("signup.submit")
    }
  }

  @ViewBuilder
  private var handleStatusRow: some View {
    if handle.isEmpty {
      EmptyView()
    } else if !validHandleFormat(handle) {
      Label("3–20 lowercase letters, digits, or underscores", systemImage: "exclamationmark.circle")
        .foregroundStyle(.red)
        .font(.footnote)
    } else if let err = handleProbeError {
      Text(err).foregroundStyle(.orange).font(.footnote)
    } else {
      switch handleAvailable {
      case .some(true):
        Label("Available", systemImage: "checkmark.circle.fill")
          .foregroundStyle(.green).font(.footnote)
      case .some(false):
        Label("Already taken", systemImage: "xmark.circle.fill")
          .foregroundStyle(.red).font(.footnote)
      case .none:
        ProgressView().controlSize(.mini)
      }
    }
  }

  // MARK: behavior

  private var canSubmit: Bool {
    validHandleFormat(handle) && handleAvailable == true
      && !displayName.trimmingCharacters(in: .whitespaces).isEmpty
  }

  private func goBack() {
    if let prev = Step(rawValue: step.rawValue - 1) {
      step = prev
    }
  }

  private func advance(from current: Step) {
    if let next = Step(rawValue: current.rawValue + 1) {
      step = next
    }
  }

  private func loadTosIfNeeded(force: Bool = false) async {
    if tos != nil, !force { return }
    tosLoadError = nil
    do {
      tos = try await UsersAPI.fetchTOS()
    } catch {
      tosLoadError = error.localizedDescription
    }
  }

  private func scheduleHandleProbe() {
    handleProbeTask?.cancel()
    handleAvailable = nil
    handleProbeError = nil
    let candidate = handle
    guard validHandleFormat(candidate) else { return }
    handleProbeTask = Task {
      try? await Task.sleep(nanoseconds: 350_000_000)
      if Task.isCancelled || candidate != handle { return }
      do {
        let avail = try await UsersAPI.checkHandleAvailable(candidate, auth: auth)
        if Task.isCancelled || candidate != handle { return }
        handleAvailable = avail
      } catch is CancellationError {
        return
      } catch {
        if Task.isCancelled || candidate != handle { return }
        handleProbeError = "couldn't check handle right now"
      }
    }
  }

  private func submit() async {
    submitError = nil
    submitting = true
    defer { submitting = false }

    // The handle step's submit button is gated by canSubmit, which requires
    // the user to have advanced through the ToS step — and the ToS step's
    // Continue button is gated by tosAccepted, which is gated by tosScrolledToEnd,
    // which only flips after `tos` loaded. So `tos` is non-nil here by
    // construction; force-unwrap is the right shape, not a nil-coalesce default.
    let submission = UsersAPI.ProfileSubmission(
      handle: handle,
      handleDisplay: handle,  // v0: handle_display = handle (no mixed-case input yet)
      displayName: displayName.trimmingCharacters(in: .whitespaces),
      dob: isoDateString(from: dob),
      tosVersion: tos!.version
    )

    do {
      try await UsersAPI.submitProfile(submission, auth: auth)
    } catch UsersAPI.APIError.handleTaken {
      handleAvailable = false
      submitError = "That handle was just taken. Pick another."
      return
    } catch UsersAPI.APIError.profileComplete {
      // Profile already exists — server treats this as a forward-step
      // signal (per the triage decision). Fall through to avatar upload.
    } catch {
      submitError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
      return
    }

    // Avatar upload + post.
    guard let userID = auth.userID, let img = selfieImage,
      let jpeg = jpegEncode(img, maxBytes: 2 * 1024 * 1024)
    else {
      submitError = "Couldn't process avatar — try recapturing."
      return
    }

    do {
      let path = try await AvatarStorage.upload(jpegData: jpeg, userID: userID)
      try await UsersAPI.submitAvatar(path: path, auth: auth)
    } catch {
      submitError = "Couldn't upload avatar: \(error.localizedDescription)"
      return
    }

    await auth.refreshSignupState()
  }
}

// MARK: helpers

private func validHandleFormat(_ s: String) -> Bool {
  guard (3...20).contains(s.count) else { return false }
  return s.allSatisfy(isHandleChar)
}

private func isHandleChar(_ c: Character) -> Bool {
  return c.isASCII && (c.isLowercase || c.isNumber || c == "_")
}

private func is18OrOlder(_ dob: Date) -> Bool {
  // -18y from now is always representable; force-unwrap surfaces the
  // impossible case as a crash in dev rather than silently allowing
  // under-18 by falling through to Date().
  let cutoff = Calendar.current.date(byAdding: .year, value: -18, to: Date())!
  return dob <= cutoff
}

private func isoDateString(from date: Date) -> String {
  let f = DateFormatter()
  f.calendar = Calendar(identifier: .gregorian)
  f.timeZone = TimeZone(identifier: "UTC")
  f.locale = Locale(identifier: "en_US_POSIX")
  f.dateFormat = "yyyy-MM-dd"
  return f.string(from: date)
}

// jpegEncode steps quality down until the encoded image fits the storage
// bucket's 2 MiB cap, mirroring the banner-upload heuristic from #41.
private func jpegEncode(_ image: UIImage, maxBytes: Int) -> Data? {
  let qualities: [CGFloat] = [0.85, 0.7, 0.55, 0.4, 0.3]
  for q in qualities {
    if let data = image.jpegData(compressionQuality: q), data.count <= maxBytes {
      return data
    }
  }
  return image.jpegData(compressionQuality: 0.3)
}

// SelfiePickerButton: on simulator, AVCaptureSession can't open the front
// camera, so the brief calls for a PhotosPicker swap. Real-device path
// uses the system camera via UIImagePickerController.
struct SelfiePickerButton: View {
  @Binding var image: UIImage?
  @State private var photosItem: PhotosPickerItem?
  @State private var cameraOpen = false

  var body: some View {
    #if targetEnvironment(simulator)
      PhotosPicker(selection: $photosItem, matching: .images) {
        Label(image == nil ? "Pick a photo" : "Pick another", systemImage: "photo.on.rectangle")
      }
      .accessibilityIdentifier("signup.selfie.pick")
      .onChange(of: photosItem) { _, newItem in
        guard let newItem else { return }
        Task {
          if let data = try? await newItem.loadTransferable(type: Data.self),
            let ui = UIImage(data: data)
          {
            image = ui
          }
        }
      }
    #else
      Button(image == nil ? "Take selfie" : "Retake") { cameraOpen = true }
        .accessibilityIdentifier("signup.selfie.capture")
        .sheet(isPresented: $cameraOpen) {
          CameraSheet(image: $image)
        }
    #endif
  }
}

#if !targetEnvironment(simulator)
  struct CameraSheet: UIViewControllerRepresentable {
    @Binding var image: UIImage?
    @Environment(\.dismiss) private var dismiss

    func makeUIViewController(context: Context) -> UIImagePickerController {
      let p = UIImagePickerController()
      p.sourceType = .camera
      p.cameraDevice = .front
      p.delegate = context.coordinator
      return p
    }
    func updateUIViewController(_ uiViewController: UIImagePickerController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate,
      UINavigationControllerDelegate
    {
      let parent: CameraSheet
      init(_ p: CameraSheet) { self.parent = p }
      func imagePickerController(
        _ picker: UIImagePickerController,
        didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]
      ) {
        parent.image = info[.originalImage] as? UIImage
        parent.dismiss()
      }
      func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        parent.dismiss()
      }
    }
  }
#endif
