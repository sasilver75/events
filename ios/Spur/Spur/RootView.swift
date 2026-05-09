import SwiftUI

struct RootView: View {
  @Environment(AuthModel.self) private var auth
  @Environment(ProfileModel.self) private var profile

  var body: some View {
    switch auth.phase {
    case .loading:
      ProgressView().controlSize(.large)
    case .signedOut, .awaitingCode:
      SignInView()
        .onAppear { profile.reset() }
    case .signedIn:
      authenticatedView
    }
  }

  // After auth.signedIn fires we don't immediately know if the user has
  // completed the profile-setup flow (#88, ADR 0025). ProfileModel.probe
  // hits GET /me and distinguishes 200 (complete) from 409 profile_required
  // (in-flight signup). We show a spinner during the probe, and route to
  // ProfileSetupView vs the tab bar based on the result.
  @ViewBuilder
  private var authenticatedView: some View {
    switch profile.phase {
    case .unknown, .probing:
      ProgressView().controlSize(.large)
        .task { await profile.probe(auth: auth) }
    case .needed:
      ProfileSetupView()
    case .complete:
      TabView {
        ContentView()
          .tabItem { Label("Map", systemImage: "map") }
        FriendsView()
          .tabItem { Label("Friends", systemImage: "person.2") }
      }
    case .error(let message):
      VStack(spacing: 12) {
        ContentUnavailableView(
          "Couldn't reach the server",
          systemImage: "wifi.exclamationmark",
          description: Text(message))
        Button("Retry") { Task { await profile.probe(auth: auth) } }
          .buttonStyle(.borderedProminent)
      }
    }
  }
}
