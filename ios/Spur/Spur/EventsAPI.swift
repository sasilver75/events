import CoreLocation
import Foundation

enum EventsAPI {
  enum APIError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case eventFull

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .eventFull: return "This event is full"
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
    auth: AuthModel
  ) async throws -> [NearbyEvent] {
    guard let token = await auth.accessToken() else { throw APIError.noToken }

    var components = URLComponents(
      url: SupabaseConfig.serverURL.appendingPathComponent("events"),
      resolvingAgainstBaseURL: false
    )!
    components.queryItems = [
      URLQueryItem(name: "near", value: "\(near.latitude),\(near.longitude)"),
      URLQueryItem(name: "radius_m", value: String(radiusM)),
    ]

    var req = URLRequest(url: components.url!)
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
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

    enum CodingKeys: String, CodingKey {
      case title, description, category
      case startTime = "start_time"
      case endTime = "end_time"
      case lat, lon, cap
      case locationVisibility = "location_visibility"
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
