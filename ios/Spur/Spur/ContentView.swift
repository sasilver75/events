import CoreLocation
import SwiftUI

struct ContentView: View {
  @Environment(AuthModel.self) private var auth
  @State private var location = LocationManager()

  @State private var events: [NearbyEvent] = []
  @State private var hasFetched = false
  @State private var fetchError: String?
  @State private var selectedEvent: NearbyEvent?

  @State private var showingPermissionPrompt = false
  @State private var showingAttribution = false
  @State private var probeResult: String?
  @State private var probeRunning = false

  // 50 km from the requested center comfortably covers LA metro at v0 scale,
  // so a single fetch on map appear is enough — no viewport-based refetch
  // until the app expands beyond LA. See issue #10 plan for the deferral.
  private let fetchRadiusM = 50_000
  private let losAngeles = CLLocationCoordinate2D(latitude: 34.0522, longitude: -118.2437)

  var body: some View {
    ZStack(alignment: .top) {
      MapView(
        styleURL: Bundle.main.mapStyleLightURL,
        center: losAngeles,
        zoom: 11,
        events: events,
        onSelect: { selectedEvent = $0 }
      )
      .ignoresSafeArea()

      topBar

      if hasFetched && events.isEmpty && fetchError == nil {
        emptyBanner
      }

      if let err = fetchError {
        errorBanner(err)
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
      EventDetailSheet(event: event)
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
    await fetch(near: losAngeles)
  }

  private func fetch(near coord: CLLocationCoordinate2D) async {
    do {
      let result = try await EventsAPI.fetchNearby(
        near: coord, radiusM: fetchRadiusM, auth: auth)
      events = result
      fetchError = nil
      hasFetched = true
    } catch {
      fetchError = error.localizedDescription
      hasFetched = true
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
