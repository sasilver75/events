import Foundation

enum FriendsAPI {
  enum APIError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case alreadyFriends
    case requestAlreadySent
    case requestPendingFromThem
    case notFound

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .alreadyFriends: return "You're already friends."
      case .requestAlreadySent: return "You've already sent them a request."
      case .requestPendingFromThem:
        return "They've already sent you a request — accept it from your inbox."
      case .notFound: return "Not found."
      }
    }
  }

  struct Friend: Decodable, Identifiable {
    let userID: String
    let friendID: String
    let createdAt: Date
    let displayName: String

    var id: String { friendID }

    enum CodingKeys: String, CodingKey {
      case userID = "user_id"
      case friendID = "friend_id"
      case createdAt = "created_at"
      case displayName = "display_name"
    }
  }

  struct Request: Decodable, Identifiable {
    let requester: String
    let recipient: String
    let createdAt: Date
    let displayName: String

    // Stable id from whichever side isn't the caller; fine for List diffing.
    var id: String { "\(requester)→\(recipient)" }

    enum CodingKeys: String, CodingKey {
      case requester, recipient
      case createdAt = "created_at"
      case displayName = "display_name"
    }
  }

  struct RequestsList: Decodable {
    let incoming: [Request]
    let outgoing: [Request]
  }

  struct Candidate: Decodable, Identifiable {
    let userID: String
    let displayName: String

    var id: String { userID }

    enum CodingKeys: String, CodingKey {
      case userID = "user_id"
      case displayName = "display_name"
    }
  }

  static func listFriends(auth: AuthModel) async throws -> [Friend] {
    try await getJSON(path: "friends", auth: auth)
  }

  static func listRequests(auth: AuthModel) async throws -> RequestsList {
    try await getJSON(path: "friends/requests", auth: auth)
  }

  static func searchCandidates(query: String, auth: AuthModel) async throws -> [Candidate] {
    var components = URLComponents(
      url: SupabaseConfig.serverURL.appendingPathComponent("friends/candidates"),
      resolvingAgainstBaseURL: false
    )!
    components.queryItems = [URLQueryItem(name: "q", value: query)]
    return try await getJSON(url: components.url!, auth: auth)
  }

  static func sendRequest(recipientID: String, auth: AuthModel) async throws {
    struct Body: Encodable {
      let recipient_id: String
    }
    let (code, data) = try await mutate(
      path: "friends/requests",
      method: "POST",
      body: Body(recipient_id: recipientID),
      auth: auth
    )
    if code == 201 { return }
    if code == 409, let err = try? JSONDecoder().decode([String: String].self, from: data) {
      switch err["error"] {
      case "already_friends": throw APIError.alreadyFriends
      case "request_already_sent": throw APIError.requestAlreadySent
      case "request_pending_from_them": throw APIError.requestPendingFromThem
      default: break
      }
    }
    if code == 404 { throw APIError.notFound }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }

  static func acceptRequest(requesterID: String, auth: AuthModel) async throws {
    try await mutateNoBody(
      path: "friends/requests/\(requesterID)/accept", method: "POST", auth: auth)
  }

  static func rejectRequest(requesterID: String, auth: AuthModel) async throws {
    try await mutateNoBody(
      path: "friends/requests/\(requesterID)", method: "DELETE", auth: auth)
  }

  static func withdrawRequest(recipientID: String, auth: AuthModel) async throws {
    try await mutateNoBody(
      path: "friends/requests/sent/\(recipientID)", method: "DELETE", auth: auth)
  }

  static func unfriend(friendID: String, auth: AuthModel) async throws {
    try await mutateNoBody(
      path: "friends/\(friendID)", method: "DELETE", auth: auth)
  }

  // MARK: helpers

  private static func getJSON<T: Decodable>(path: String, auth: AuthModel) async throws -> T {
    try await getJSON(url: SupabaseConfig.serverURL.appendingPathComponent(path), auth: auth)
  }

  private static func getJSON<T: Decodable>(url: URL, auth: AuthModel) async throws -> T {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: url)
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
      return try decoder.decode(T.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  private static func mutate<B: Encodable>(
    path: String, method: String, body: B, auth: AuthModel
  ) async throws -> (Int, Data) {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent(path))
    req.httpMethod = method
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      req.httpBody = try JSONEncoder().encode(body)
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
    return ((resp as? HTTPURLResponse)?.statusCode ?? -1, data)
  }

  private static func mutateNoBody(path: String, method: String, auth: AuthModel) async throws {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent(path))
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
    if code == 200 || code == 204 { return }
    if code == 404 { throw APIError.notFound }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }
}
