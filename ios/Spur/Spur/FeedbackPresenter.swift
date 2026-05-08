import Foundation
import Observation
import UIKit
import UserNotifications

// FeedbackPresenter is the bridge from a notification tap (which arrives
// via UNUserNotificationCenterDelegate, outside the SwiftUI view tree)
// to a SwiftUI sheet.
//
// The notification carries `event_id` in its userInfo. When the user
// taps it, the delegate stores that id on a shared FeedbackPresenter
// singleton; ContentView observes the singleton and presents
// FeedbackSheet for the requested event.
//
// Singleton (vs. environment-injected): the
// UNUserNotificationCenterDelegate is registered against the UIApplication,
// not a SwiftUI view, so it can't reach into @Environment. A shared
// instance is the simplest bridge that survives app lifecycle (cold
// launch from a tap arrives at didFinishLaunching, not later view
// rendering).
@Observable
@MainActor
final class FeedbackPresenter {
  static let shared = FeedbackPresenter()

  // pendingEventID is the event the user just tapped a feedback notification
  // for. ContentView watches this; once it presents the sheet, it sets the
  // value back to nil so a subsequent tap on the same notification (rare
  // but possible if the sheet was dismissed) re-triggers presentation.
  var pendingEventID: String?

  private init() {}

  func handleTap(eventID: String) {
    pendingEventID = eventID
  }
}

// AppDelegate is the smallest possible UIApplicationDelegate — its sole
// job today is to register a UNUserNotificationCenterDelegate that
// forwards taps to FeedbackPresenter. Wired in SpurApp via
// @UIApplicationDelegateAdaptor.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
  func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    UNUserNotificationCenter.current().delegate = self
    return true
  }

  // userNotificationCenter(_:willPresent:withCompletionHandler:) is called
  // when a notification arrives while the app is in the foreground. By
  // default iOS suppresses the banner; we explicitly request the banner +
  // sound + list so a user who is already in the app at end_time sees
  // the ping AND finds it in Notification Center if they miss the banner.
  // Without `.list`, the banner flashes briefly and leaves no trace —
  // tapping a vanished banner is impossible (issue #35 manual test
  // surfaced this).
  func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler:
      @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .sound, .list])
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    didReceive response: UNNotificationResponse,
    withCompletionHandler completionHandler: @escaping () -> Void
  ) {
    let userInfo = response.notification.request.content.userInfo
    if let eventID = userInfo[NotificationScheduler.payloadEventIDKey] as? String {
      Task { @MainActor in
        FeedbackPresenter.shared.handleTap(eventID: eventID)
      }
    }
    completionHandler()
  }
}
