import SwiftUI

@main
struct SpurApp: App {
  @State private var auth = AuthModel()

  var body: some Scene {
    WindowGroup {
      RootView()
        .environment(auth)
    }
  }
}
