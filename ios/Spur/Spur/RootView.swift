import SwiftUI

struct RootView: View {
  @Environment(AuthModel.self) private var auth

  var body: some View {
    switch auth.phase {
    case .loading:
      ProgressView()
        .controlSize(.large)
    case .signedIn:
      ContentView()
    case .signedOut, .awaitingCode:
      SignInView()
    }
  }
}
