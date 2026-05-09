import SwiftUI

@main
struct SpurApp: App {
  @State private var auth = AuthModel()
  @State private var profile = ProfileModel()

  var body: some Scene {
    WindowGroup {
      RootView()
        .environment(auth)
        .environment(profile)
    }
  }
}
