import CoreLocation
import Foundation
import Supabase

// Banner image upload + public-URL resolution for the event-banners
// bucket (#41). The bucket is public-read so we can use getPublicURL
// directly — no signed-URL ceremony, and the Supabase CDN can cache.
//
// Path layout: "{userID}/{uuid}.jpg". The leading user-id segment is
// what storage RLS keys against (storage.foldername(name)[1] =
// auth.uid()::text), so anyone can read but only the owner can write
// under their prefix.
enum BannerStorage {
  static let bucket = "event-banners"

  static func upload(jpegData: Data, userID: UUID) async throws -> String {
    let path = "\(userID.uuidString.lowercased())/\(UUID().uuidString.lowercased()).jpg"
    _ = try await SupabaseConfig.shared.storage
      .from(bucket)
      .upload(
        path,
        data: jpegData,
        options: FileOptions(contentType: "image/jpeg", upsert: false)
      )
    return path
  }

  static func publicURL(forPath path: String) -> URL? {
    try? SupabaseConfig.shared.storage.from(bucket).getPublicURL(path: path)
  }
}

enum EventsAPI {
  enum APIError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case eventFull
    case notAtEvent(distanceM: Double, accuracyM: Double)

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .eventFull: return "This event is full"
      case .notAtEvent:
        return "You're too far from the pin to check in — get closer or check your GPS signal"
      }
    }
  }

  struct CommitState: Decodable {
    let commitCount: Int
    let committedByMe: Bool

    enum CodingKeys: String, CodingKey {
      case commitCount = "commit_count"
      case committedByMe = "committed_by_me"
    }
  }

  static func fetchNearby(
    near: CLLocationCoordinate2D,
    radiusM: Int,
    from: Date? = nil,
    to: Date? = nil,
    auth: AuthModel
  ) async throws -> [NearbyEvent] {
    guard let token = await auth.accessToken() else { throw APIError.noToken }

    var components = URLComponents(
      url: SupabaseConfig.serverURL.appendingPathComponent("events"),
      resolvingAgainstBaseURL: false
    )!
    let rfc = ISO8601DateFormatter()
    rfc.formatOptions = [.withInternetDateTime]
    var items: [URLQueryItem] = [
      URLQueryItem(name: "near", value: "\(near.latitude),\(near.longitude)"),
      URLQueryItem(name: "radius_m", value: String(radiusM)),
    ]
    if let from { items.append(URLQueryItem(name: "from", value: rfc.string(from: from))) }
    if let to { items.append(URLQueryItem(name: "to", value: rfc.string(from: to))) }
    components.queryItems = items

    var req = URLRequest(url: components.url!)
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch let urlErr as URLError where urlErr.code == .cancelled {
      throw CancellationError()
    } catch {
      throw APIError.transport(error.localizedDescription)
    }

    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    guard code == 200 else {
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }

    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601withFractionalSeconds
    do {
      return try decoder.decode([NearbyEvent].self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  struct CreateInput: Encodable {
    let title: String
    let description: String
    let category: String
    let startTime: Date
    let endTime: Date
    let lat: Double
    let lon: Double
    let cap: Int?
    let locationVisibility: String
    // β-Event fields. Both nil for α; both set for β. Server enforces
    // the pair invariant + threshold ≥ 2 + cap ≥ threshold + deadline
    // bounds; client validates before submit so the user gets feedback
    // without a round-trip.
    let tipThreshold: Int?
    let tipDeadline: Date?
    let bannerPath: String?

    enum CodingKeys: String, CodingKey {
      case title, description, category
      case startTime = "start_time"
      case endTime = "end_time"
      case lat, lon, cap
      case locationVisibility = "location_visibility"
      case tipThreshold = "tip_threshold"
      case tipDeadline = "tip_deadline"
      case bannerPath = "banner_path"
    }
  }

  static func create(_ input: CreateInput, auth: AuthModel) async throws {
    guard let token = await auth.accessToken() else { throw APIError.noToken }

    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent("events"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let encoder = JSONEncoder()
    encoder.dateEncodingStrategy = .iso8601
    do {
      req.httpBody = try encoder.encode(input)
    } catch {
      throw APIError.transport("encode body: \(error.localizedDescription)")
    }

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }

    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    guard code == 201 else {
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
  }

  struct CheckIn: Decodable {
    let recordedAt: Date

    enum CodingKeys: String, CodingKey {
      case recordedAt = "recorded_at"
    }
  }

  // checkIn POSTs the user's current GPS reading; the server applies the
  // accuracy-aware geofence rule (ADR 0011). 409 not_at_event surfaces as
  // APIError.notAtEvent so the caller can show the canonical message.
  static func checkIn(
    eventID: String,
    lat: Double,
    lon: Double,
    horizontalAccuracyM: Double,
    auth: AuthModel
  ) async throws -> CheckIn {
    guard let token = await auth.accessToken() else { throw APIError.noToken }

    let url = SupabaseConfig.serverURL
      .appendingPathComponent("events")
      .appendingPathComponent(eventID)
      .appendingPathComponent("checkin")

    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    struct Body: Encodable {
      let lat: Double
      let lon: Double
      let horizontal_accuracy_m: Double
    }
    do {
      req.httpBody = try JSONEncoder().encode(
        Body(lat: lat, lon: lon, horizontal_accuracy_m: horizontalAccuracyM))
    } catch {
      throw APIError.transport("encode body: \(error.localizedDescription)")
    }

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }

    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    if code == 409 {
      struct NotAtEventBody: Decodable {
        let error: String
        let distanceM: Double
        let accuracyM: Double

        enum CodingKeys: String, CodingKey {
          case error
          case distanceM = "distance_m"
          case accuracyM = "accuracy_m"
        }
      }
      if let body = try? JSONDecoder().decode(NotAtEventBody.self, from: data),
        body.error == "not_at_event"
      {
        throw APIError.notAtEvent(distanceM: body.distanceM, accuracyM: body.accuracyM)
      }
    }
    guard code == 200 else {
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601withFractionalSeconds
    do {
      return try decoder.decode(CheckIn.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  static func commit(eventID: String, auth: AuthModel) async throws -> CommitState {
    try await commitMutation(eventID: eventID, method: "POST", auth: auth)
  }

  static func withdraw(eventID: String, auth: AuthModel) async throws -> CommitState {
    try await commitMutation(eventID: eventID, method: "DELETE", auth: auth)
  }

  private static func commitMutation(
    eventID: String, method: String, auth: AuthModel
  ) async throws -> CommitState {
    guard let token = await auth.accessToken() else { throw APIError.noToken }

    let url = SupabaseConfig.serverURL
      .appendingPathComponent("events")
      .appendingPathComponent(eventID)
      .appendingPathComponent("commit")

    var req = URLRequest(url: url)
    req.httpMethod = method
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }

    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    if code == 409, let body = try? JSONDecoder().decode([String: String].self, from: data),
      body["error"] == "event_full"
    {
      throw APIError.eventFull
    }
    guard code == 200 else {
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
    do {
      return try JSONDecoder().decode(CommitState.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }
}

// Postgres TIMESTAMPTZ marshals as RFC3339 with fractional seconds (e.g.
// "2026-05-06T16:00:00.123456Z"). Foundation's stock .iso8601 chokes on the
// fractional part, so we use a custom strategy that accepts both shapes.
extension JSONDecoder.DateDecodingStrategy {
  static var iso8601withFractionalSeconds: JSONDecoder.DateDecodingStrategy {
    .custom { decoder in
      let s = try decoder.singleValueContainer().decode(String.self)
      let withFrac = ISO8601DateFormatter()
      withFrac.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
      if let d = withFrac.date(from: s) { return d }
      let plain = ISO8601DateFormatter()
      plain.formatOptions = [.withInternetDateTime]
      if let d = plain.date(from: s) { return d }
      throw DecodingError.dataCorrupted(
        .init(codingPath: decoder.codingPath, debugDescription: "bad ISO8601: \(s)"))
    }
  }
}
