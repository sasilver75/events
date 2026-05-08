import SwiftUI

struct RootView: View {
  @Environment(AuthModel.self) private var auth

  var body: some View {
    switch auth.phase {
    case .loading:
      ProgressView()
        .controlSize(.large)
    case .signedIn:
      TabView {
        ContentView()
          .tabItem { Label("Map", systemImage: "map") }
        FriendsView()
          .tabItem { Label("Friends", systemImage: "person.2") }
      }
    case .signedOut, .awaitingCode:
      SignInView()
    }
  }
}
