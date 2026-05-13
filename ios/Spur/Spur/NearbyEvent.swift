import CoreLocation
import Foundation

// Decoded shape of GET /events?near. Mirrors server/internal/events/events.go.
// `lat`/`lon` are already visibility-resolved server-side (public → exact;
// fuzzed + non-Committed → display_geom; fuzzed + Committed → exact), so the
// client treats them as the authoritative pin position with no further logic.
struct NearbyEvent: Decodable, Identifiable, Hashable {
  let id: String
  let title: String
  let description: String
  let category: String
  let startTime: Date
  let endTime: Date
  let lat: Double
  let lon: Double
  let cap: Int?
  var commitCount: Int
  var committedByMe: Bool
  var checkedInByMe: Bool
  let state: String
  let bannerPath: String?
  // α-always-true once Committed; β-true iff post-Tip. Derived server-side
  // so iOS doesn't reproduce the lock semantics. Gates the chat affordance
  // in EventDetailSheet (#65).
  let chatUnlocked: Bool

  enum CodingKeys: String, CodingKey {
    case id, title, description, category
    case startTime = "start_time"
    case endTime = "end_time"
    case lat, lon, cap
    case commitCount = "commit_count"
    case committedByMe = "committed_by_me"
    case checkedInByMe = "checked_in_by_me"
    case state
    case bannerPath = "banner_path"
    case chatUnlocked = "chat_unlocked"
  }

  var coordinate: CLLocationCoordinate2D {
    CLLocationCoordinate2D(latitude: lat, longitude: lon)
  }

  var categoryEnum: EventCategory { .from(category) }

  enum Urgency {
    case live
    case soon
    case later
  }

  func urgency(now: Date) -> Urgency {
    if startTime <= now { return .live }
    if startTime.timeIntervalSince(now) <= 2 * 60 * 60 { return .soon }
    return .later
  }
}
