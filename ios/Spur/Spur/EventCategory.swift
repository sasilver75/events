import SwiftUI

// Event categories from CONTEXT.md taxonomy. Server returns the display string;
// `from(rawValue:)` is lenient — unknown strings collapse to `.other` so a
// future server-side category addition doesn't crash the iOS client.
enum EventCategory: String, CaseIterable {
  case sports = "Sports"
  case foodDrink = "Food/Drink"
  case music = "Music"
  case outdoors = "Outdoors"
  case games = "Games"
  case social = "Social"
  case creative = "Creative"
  case wellness = "Wellness"
  case networking = "Networking"
  case other = "Other"

  static func from(_ raw: String) -> EventCategory {
    EventCategory(rawValue: raw) ?? .other
  }

  var symbolName: String {
    switch self {
    case .sports: return "basketball.fill"
    case .foodDrink: return "cup.and.saucer.fill"
    case .music: return "music.note"
    case .outdoors: return "leaf.fill"
    case .games: return "gamecontroller.fill"
    case .social: return "person.2.fill"
    case .creative: return "paintbrush.fill"
    case .wellness: return "heart.fill"
    case .networking: return "briefcase.fill"
    case .other: return "sparkles"
    }
  }

  var color: Color {
    switch self {
    case .sports: return .orange
    case .foodDrink: return .brown
    case .music: return .pink
    case .outdoors: return .green
    case .games: return .purple
    case .social: return .blue
    case .creative: return .yellow
    case .wellness: return .red
    case .networking: return .indigo
    case .other: return .gray
    }
  }
}
