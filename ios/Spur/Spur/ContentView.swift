import CoreLocation
import SwiftUI

struct ContentView: View {
  @Environment(AuthModel.self) private var auth
  @State private var probeResult: String?
  @State private var probeRunning = false
  @State private var showingAttribution = false

  private let losAngeles = CLLocationCoordinate2D(latitude: 34.0522, longitude: -118.2437)

  var body: some View {
    ZStack(alignment: .top) {
      MapView(
        styleURL: Bundle.main.mapStyleLightURL,
        center: losAngeles,
        zoom: 11
      )
      .ignoresSafeArea()

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
