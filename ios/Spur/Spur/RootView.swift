import SwiftUI

struct RootView: View {
  @Environment(AuthModel.self) private var auth

  var body: some View {
    switch auth.phase {
    case .loading:
      ProgressView().controlSize(.large)
    case .signedOut, .awaitingCode:
      SignInView()
    case .signedIn(_, let signup):
      switch signup {
      case .unknown:
        // First post-OTP frame; the probe is in flight. Show a spinner
        // rather than flash the empty TabView or signup form.
        ProgressView().controlSize(.large)
      case .needsProfile:
        SignupView(initialStep: .selfie)
      case .needsAvatar:
        // Resume case: user has a profile but no avatar (crash mid-flow,
        // reinstall, etc). Drop them back at selfie capture so the flow
        // is identical.
        SignupView(initialStep: .selfie)
      case .complete:
        TabView {
          ContentView()
            .tabItem { Label("Map", systemImage: "map") }
          FriendsView()
            .tabItem { Label("Friends", systemImage: "person.2") }
        }
      }
    }
  }
}
