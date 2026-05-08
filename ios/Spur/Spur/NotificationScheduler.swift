import Foundation
import UserNotifications

// NotificationScheduler wraps the small slice of UNUserNotificationCenter
// the post-event feedback flow needs (#35): permission request at first
// check-in, schedule a local "How was Event?" reminder for end_time, and
// cancel a pending reminder when an Event is cancelled.
//
// All ids are deterministic on event_id so a re-check-in on the same
// Event idempotently overwrites the existing request rather than
// stacking duplicate notifications. Cancellation by id Just Works for
// the same reason.
//
// APNs / server-pushed reminders are out of scope (issue #35 §Out of
// scope, blocked on #12 — Apple Developer Program enrollment). Once
// APNs lands, this scheduler becomes the local fallback for at-Done
// pings; the +6h/+18h re-engagement nudges in the followup will fire
// from the server.
enum NotificationScheduler {
  static let payloadEventIDKey = "event_id"

  static func notificationID(eventID: String) -> String { "feedback-\(eventID)" }

  // requestAuthorizationIfNeeded is a no-op when the user has already
  // granted or denied. Called from EventDetailSheet.tapCheckIn on
  // success so the prompt lands in the moment that motivates it
  // ("you're at the Event, we'll ping you when it ends") rather than
  // at app launch.
  static func requestAuthorizationIfNeeded() async {
    let center = UNUserNotificationCenter.current()
    let settings = await center.notificationSettings()
    guard settings.authorizationStatus == .notDetermined else { return }
    _ = try? await center.requestAuthorization(options: [.alert, .sound])
  }

  static func scheduleEndOfEventReminder(eventID: String, eventTitle: String, endTime: Date) async {
    // Notifications scheduled for the past don't fire; cap at "fire in
    // a few seconds" so a user who checks in after end_time (e.g.
    // late-arriving slow-check-in flow) still gets the reminder once
    // they've checked in.
    let fireAt = max(endTime, Date().addingTimeInterval(2))

    let content = UNMutableNotificationContent()
    content.title = "How was \(eventTitle)?"
    content.body = "Tap to leave feedback."
    content.userInfo = [payloadEventIDKey: eventID]
    content.sound = .default

    let dateComponents = Calendar.current.dateComponents(
      [.year, .month, .day, .hour, .minute, .second],
      from: fireAt)
    let trigger = UNCalendarNotificationTrigger(dateMatching: dateComponents, repeats: false)
    let req = UNNotificationRequest(
      identifier: notificationID(eventID: eventID),
      content: content,
      trigger: trigger)
    do {
      try await UNUserNotificationCenter.current().add(req)
    } catch {
      // Non-fatal: a denied authorization or a malformed request shouldn't
      // surface as a check-in failure. The past-events list (#36) will
      // be the re-entry path for users who never see a notification.
    }
  }

  static func cancelEndOfEventReminder(eventID: String) {
    UNUserNotificationCenter.current()
      .removePendingNotificationRequests(withIdentifiers: [notificationID(eventID: eventID)])
  }
}
