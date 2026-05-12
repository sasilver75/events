import CoreLocation
import Foundation
import Observation

enum AppTab: Hashable {
  case map
  case yourEvents
  case friends
}

// AppNavigation is the shared state that lets non-Map surfaces (Your Events
// rows, future notification taps) deeplink into the Map — recentering it on
// a pin and presenting the EventDetailSheet. The Map view tree owns the
// presentation; this object is the message-passing channel.
//
// Lifecycle: a single instance lives on RootView and is injected into the
// environment. Producers (Your Events) call `openEvent(_:)`; the consumer
// (ContentView) observes `pendingDeeplink` via .onChange and clears it after
// consuming so the same target can be re-deeplinked later.
@MainActor
@Observable
final class AppNavigation {
  var selectedTab: AppTab = .map
  var pendingDeeplink: Deeplink?

  // Identifiable so .onChange fires on every new request even when the same
  // event is tapped twice in a row — a fresh UUID forces SwiftUI to treat
  // the value as changed.
  struct Deeplink: Identifiable, Equatable {
    let id = UUID()
    let eventID: String
    let coordinate: CLLocationCoordinate2D
    let title: String
    let category: String
    let startTime: Date
    let endTime: Date
    let state: String

    static func == (lhs: Deeplink, rhs: Deeplink) -> Bool { lhs.id == rhs.id }
  }

  func openEvent(_ event: EventsAPI.MyCommitEvent) {
    pendingDeeplink = Deeplink(
      eventID: event.id,
      coordinate: event.coordinate,
      title: event.title,
      category: event.category,
      startTime: event.startTime,
      endTime: event.endTime,
      state: event.state
    )
    selectedTab = .map
  }
}
