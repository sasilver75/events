import CoreLocation
import SwiftUI

struct ContentView: View {
  @Environment(AuthModel.self) private var auth
  @Environment(AppNavigation.self) private var nav
  @State private var location = LocationManager()
  @State private var mapFlyTo: MapView.FlyTo?
  // FeedbackPresenter.shared is the singleton bridge from
  // UNUserNotificationCenterDelegate (which lives outside the SwiftUI
  // view tree) to a sheet presented inside the tree.
  @State private var feedback = FeedbackPresenter.shared

  @State private var events: [NearbyEvent] = []
  @State private var hasFetched = false
  @State private var fetchError: String?
  @State private var selectedEvent: NearbyEvent?
  @State private var feedbackEventID: String?

  @State private var showingPermissionPrompt = false
  @State private var showingAttribution = false
  @State private var probeResult: String?
  @State private var probeRunning = false
  @State private var viewportCenter: CLLocationCoordinate2D = Self.losAngeles
  @State private var createSheetRequest: CreateSheetRequest?
  @State private var hoursAhead: Double = Self.maxHoursAhead
  @State private var sliderRefetchTask: Task<Void, Never>?

  private static let maxHoursAhead: Double = 72

  // Identifiable wrapper so .sheet(item:) carries the chosen center alongside
  // the present-sheet trigger — bypasses the SwiftUI transaction race where
  // setting two sibling @State values (a flag + a payload) in one button
  // action can present the sheet with a stale payload.
  private struct CreateSheetRequest: Identifiable {
    let id = UUID()
    let center: CLLocationCoordinate2D
  }
  private struct FeedbackSheetRequest: Identifiable { let id: String }

  // 50 km from the requested center comfortably covers LA metro at v0 scale,
  // so a single fetch on map appear is enough — no viewport-based refetch
  // until the app expands beyond LA. See issue #10 plan for the deferral.
  private let fetchRadiusM = 50_000
  private static let losAngeles = CLLocationCoordinate2D(latitude: 34.0522, longitude: -118.2437)

  var body: some View {
    ZStack(alignment: .top) {
      MapView(
        styleURL: Bundle.main.mapStyleLightURL,
        center: Self.losAngeles,
        zoom: 11,
        events: events,
        onSelect: { selectedEvent = $0 },
        centerBinding: $viewportCenter,
        flyTo: mapFlyTo
      )
      .ignoresSafeArea()

      topBar

      if hasFetched && events.isEmpty && fetchError == nil {
        emptyBanner
      }

      if let err = fetchError {
        errorBanner(err)
      }

      VStack {
        Spacer()
        TimeWindowSlider(
          hoursAhead: $hoursAhead,
          maxHours: Self.maxHoursAhead,
          onEditingChanged: { editing in
            if !editing { scheduleSliderRefetch() }
          }
        )
        .padding(.bottom, 24)
      }
    }
    .task {
      await beginLocationFlow()
    }
    .onChange(of: location.authorizationStatus) { _, _ in
      Task { await reactToAuthChange() }
    }
    .onChange(of: location.lastFix?.latitude) { _, _ in
      Task { await fetchOnceWithFix() }
    }
    .onChange(of: location.lastError) { _, err in
      // CoreLocation can fail to deliver a fix even after authorization (sim
      // with no location set, real device with location services off).
      // Fall back to the LA center so the user still sees Events.
      guard err != nil else { return }
      Task { await fetchWithFallback() }
    }
    .onChange(of: nav.pendingDeeplink) { _, link in
      guard let link else { return }
      consumeDeeplink(link)
    }
    .sheet(isPresented: $showingPermissionPrompt) {
      LocationPermissionView(
        onAllow: {
          showingPermissionPrompt = false
          location.requestWhenInUseAuthorization()
        },
        onSkip: {
          showingPermissionPrompt = false
          Task { await fetchWithFallback() }
        }
      )
      .interactiveDismissDisabled()
    }
    .sheet(item: $selectedEvent) { event in
      EventDetailSheet(
        event: event,
        onCommitStateChanged: { commitCount, committedByMe in
          if let idx = events.firstIndex(where: { $0.id == event.id }) {
            events[idx].commitCount = commitCount
            events[idx].committedByMe = committedByMe
          }
        },
        onCheckedIn: {
          if let idx = events.firstIndex(where: { $0.id == event.id }) {
            events[idx].checkedInByMe = true
          }
        }
      )
    }
    .sheet(item: $createSheetRequest) { request in
      CreateEventSheet(
        initialCenter: request.center,
        onCreated: { Task { await refetchAfterCreate() } })
    }
    .sheet(
      item: Binding(
        get: { feedbackEventID.map { FeedbackSheetRequest(id: $0) } },
        set: { feedbackEventID = $0?.id })
    ) { req in
      FeedbackSheet(eventID: req.id)
    }
    .onChange(of: feedback.pendingEventID) { _, eventID in
      // Drain the singleton: assigning to feedbackEventID presents the
      // sheet; clearing pendingEventID lets a future tap re-trigger.
      if let eventID {
        feedbackEventID = eventID
        feedback.pendingEventID = nil
      }
    }
    .alert(
      "Server probe",
      isPresented: Binding(
        get: { probeResult != nil },
        set: { if !$0 { probeResult = nil } }
      ),
      presenting: probeResult
    ) { _ in
      Button("OK") { probeResult = nil }
    } message: { result in
      Text(result)
    }
    .sheet(isPresented: $showingAttribution) {
      AttributionView()
    }
  }

  // MARK: subviews

  private var topBar: some View {
    HStack(alignment: .top) {
      Text("Spur — Los Angeles")
        .font(.title2)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(.regularMaterial, in: Capsule())

      Spacer()

      Button {
        createSheetRequest = CreateSheetRequest(center: viewportCenter)
      } label: {
        Image(systemName: "plus")
          .font(.title2)
          .padding(8)
          .background(.regularMaterial, in: Circle())
      }
      .accessibilityIdentifier("topBar.create")

      Menu {
        Button {
          Task { await pingMe() }
        } label: {
          Label("Ping /me", systemImage: "network")
        }
        .disabled(probeRunning)

        Button {
          showingAttribution = true
        } label: {
          Label("Attribution", systemImage: "info.circle")
        }

        Button(role: .destructive) {
          Task { await auth.signOut() }
        } label: {
          Label("Sign out", systemImage: "rectangle.portrait.and.arrow.right")
        }
      } label: {
        Image(systemName: "person.crop.circle")
          .font(.title2)
          .padding(8)
          .background(.regularMaterial, in: Circle())
      }
    }
    .padding(.horizontal, 16)
    .padding(.top, 8)
  }

  private var emptyBanner: some View {
    Text("No Events nearby right now.")
      .font(.subheadline)
      .padding(.horizontal, 14)
      .padding(.vertical, 8)
      .background(.regularMaterial, in: Capsule())
      .padding(.top, 56)
  }

  private func errorBanner(_ msg: String) -> some View {
    Text("Couldn't load Events — \(msg)")
      .font(.caption)
      .foregroundStyle(.white)
      .padding(.horizontal, 12)
      .padding(.vertical, 6)
      .background(Color.red.opacity(0.85), in: Capsule())
      .padding(.top, 56)
  }

  // MARK: location → fetch flow

  private func beginLocationFlow() async {
    switch location.authorizationStatus {
    case .notDetermined:
      showingPermissionPrompt = true
    case .authorizedWhenInUse, .authorizedAlways:
      location.requestLocation()
    case .denied, .restricted:
      await fetchWithFallback()
    @unknown default:
      await fetchWithFallback()
    }
  }

  private func reactToAuthChange() async {
    switch location.authorizationStatus {
    case .authorizedWhenInUse, .authorizedAlways:
      location.requestLocation()
    case .denied, .restricted:
      if !hasFetched { await fetchWithFallback() }
    case .notDetermined:
      break
    @unknown default:
      break
    }
  }

  private func fetchOnceWithFix() async {
    guard !hasFetched, let coord = location.lastFix else { return }
    await fetch(near: coord)
  }

  private func fetchWithFallback() async {
    guard !hasFetched else { return }
    await fetch(near: Self.losAngeles)
  }

  private func fetch(near coord: CLLocationCoordinate2D) async {
    let now = Date()
    let to = now.addingTimeInterval(hoursAhead * 3600)
    do {
      let result = try await EventsAPI.fetchNearby(
        near: coord, radiusM: fetchRadiusM, from: now, to: to, auth: auth)
      events = result
      fetchError = nil
      hasFetched = true
      // Cancel any pending end-of-event reminders for events the server
      // now reports as Cancelled. Without this, a host-cancel between
      // check-in and end_time would still ping the user (issue #35 AC).
      // The server's browse query already excludes Cancelled events from
      // the default "Live now" filter, so the practical signal is "this
      // event has dropped from the result set"; we only see the
      // explicit Cancelled state when the user widens the window.
      for ev in result where ev.state == "Cancelled" {
        NotificationScheduler.cancelEndOfEventReminder(eventID: ev.id)
      }
    } catch is CancellationError {
      return
    } catch let urlErr as URLError where urlErr.code == .cancelled {
      return
    } catch {
      fetchError = error.localizedDescription
      hasFetched = true
    }
  }

  private func refetchAfterCreate() async {
    await fetch(near: location.lastFix ?? Self.losAngeles)
  }

  // Honour a Your-Events deeplink: pan the map to the pin, then present the
  // EventDetailSheet for that Event. Prefers the in-memory NearbyEvent from
  // the current browse fetch (full projection — banner, description, commit
  // count) and falls back to a synthesised NearbyEvent from the limited
  // MyCommits projection when the deeplinked Event is outside the map's
  // window (typical for "Recent" rows whose end_time has already passed).
  private func consumeDeeplink(_ link: AppNavigation.Deeplink) {
    mapFlyTo = MapView.FlyTo(coordinate: link.coordinate, zoom: 15)
    if let existing = events.first(where: { $0.id == link.eventID }) {
      selectedEvent = existing
    } else {
      selectedEvent = NearbyEvent(
        id: link.eventID,
        title: link.title,
        description: "",
        category: link.category,
        startTime: link.startTime,
        endTime: link.endTime,
        lat: link.coordinate.latitude,
        lon: link.coordinate.longitude,
        cap: nil,
        commitCount: 0,
        committedByMe: true,
        checkedInByMe: false,
        state: link.state,
        bannerPath: nil
      )
    }
    nav.pendingDeeplink = nil
  }

  private func refetchForWindow() async {
    guard hasFetched else { return }
    await fetch(near: location.lastFix ?? viewportCenter)
  }

  // Held in @State so the next slider release can cancel any in-flight
  // fetch — without that, a fast re-drag races against the prior result.
  private func scheduleSliderRefetch() {
    sliderRefetchTask?.cancel()
    sliderRefetchTask = Task {
      await refetchForWindow()
    }
  }

  // MARK: dev probe

  private func pingMe() async {
    probeRunning = true
    defer { probeRunning = false }
    do {
      let id = try await ServerProbe.me(auth: auth)
      probeResult = "OK — user_id: \(id)"
    } catch {
      probeResult = "Failed — \(error.localizedDescription)"
    }
  }
}
