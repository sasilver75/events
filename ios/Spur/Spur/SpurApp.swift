import SwiftUI

@main
struct SpurApp: App {
  @State private var auth = AuthModel()
  // The adaptor registers a UIApplicationDelegate so we can install a
  // UNUserNotificationCenterDelegate at didFinishLaunching — necessary
  // for cold-launch-from-notification (a tap on the at-Done feedback
  // ping when the app is not running) to land before the sheet
  // presentation logic in ContentView is alive.
  @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

  var body: some Scene {
    WindowGroup {
      RootView()
        .environment(auth)
    }
  }
}
