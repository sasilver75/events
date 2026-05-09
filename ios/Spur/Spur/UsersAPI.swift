import Foundation

// UsersAPI wraps the profile-completeness server surface introduced by
// #88 (ADR 0025): the ToS fetch, handle availability probe, profile
// upsert, and avatar pointer write.
//
// The raw protocol is documented on the server side in
// server/internal/users/users.go and server/internal/legal/legal.go.
enum UsersAPI {
  enum APIError: LocalizedError, Equatable {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case handleTaken
    case handleFormat
    case handleDisplayMismatch
    case displayNameEmpty
    case dobFormat
    case dobTooRecent
    case tosVersionMismatch
    case profileRequired
    case avatarPathNotOwned

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .handleTaken: return "That handle is taken."
      case .handleFormat:
        return "Handles are 3–20 characters, lowercase letters, digits, or underscore."
      case .handleDisplayMismatch:
        return "Handle display must be the same letters as the handle."
      case .displayNameEmpty: return "Display name cannot be empty."
      case .dobFormat: return "Date of birth must be a valid date."
      case .dobTooRecent: return "You must be at least 18."
      case .tosVersionMismatch:
        return "The Terms of Service have been updated — please re-read and accept."
      case .profileRequired: return "Profile setup is required."
      case .avatarPathNotOwned: return "That image isn't owned by you."
      }
    }
  }

  // MARK: ToS

  struct ToS: Decodable {
    let version: String
    let content: String
  }

  static func fetchToS() async throws -> ToS {
    let url = SupabaseConfig.serverURL.appendingPathComponent("legal/tos")
    var req = URLRequest(url: url)
    req.cachePolicy = .reloadIgnoringLocalCacheData

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
    do {
      return try JSONDecoder().decode(ToS.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  // MARK: Handle probe

  enum HandleAvailability: Equatable {
    case available
    case taken
    case invalidFormat
  }

  // Probes HEAD /users/handle/{handle}. 200 → available, 409 → taken,
  // 422 → format invalid (server-side check matches the regex enforced on
  // POST /users/me/profile).
  static func probeHandle(_ raw: String) async throws -> HandleAvailability {
    let normalized = raw.lowercased()
    let url = SupabaseConfig.serverURL
      .appendingPathComponent("users")
      .appendingPathComponent("handle")
      .appendingPathComponent(normalized)
    var req = URLRequest(url: url)
    req.httpMethod = "HEAD"

    let resp: URLResponse
    do {
      (_, resp) = try await URLSession.shared.data(for: req)
    } catch let urlErr as URLError where urlErr.code == .cancelled {
      throw CancellationError()
    } catch {
      throw APIError.transport(error.localizedDescription)
    }
    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    switch code {
    case 200: return .available
    case 409: return .taken
    case 422: return .invalidFormat
    default: throw APIError.http(code, "")
    }
  }

  // MARK: Profile upsert

  struct ProfileInput: Encodable {
    let handle: String
    let handleDisplay: String
    let displayName: String
    let dob: String  // YYYY-MM-DD
    let tosVersion: String

    enum CodingKeys: String, CodingKey {
      case handle
      case handleDisplay = "handle_display"
      case displayName = "display_name"
      case dob
      case tosVersion = "tos_version"
    }
  }

  struct ProfileResponse: Decodable {
    let userID: String
    let handle: String
    let handleDisplay: String
    let displayName: String

    enum CodingKeys: String, CodingKey {
      case userID = "user_id"
      case handle
      case handleDisplay = "handle_display"
      case displayName = "display_name"
    }
  }

  static func upsertProfile(_ input: ProfileInput, auth: AuthModel) async throws -> ProfileResponse
  {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(
      url: SupabaseConfig.serverURL.appendingPathComponent("users/me/profile"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      req.httpBody = try JSONEncoder().encode(input)
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
    if code == 200 {
      do {
        return try JSONDecoder().decode(ProfileResponse.self, from: data)
      } catch {
        throw APIError.decode(error.localizedDescription)
      }
    }
    if let mapped = mapErrorBody(code: code, data: data) {
      throw mapped
    }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }

  // MARK: Avatar pointer

  static func setAvatarPath(_ path: String, auth: AuthModel) async throws {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(
      url: SupabaseConfig.serverURL.appendingPathComponent("users/me/avatar"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    struct Body: Encodable { let path: String }
    do {
      req.httpBody = try JSONEncoder().encode(Body(path: path))
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
    if code == 204 { return }
    if let mapped = mapErrorBody(code: code, data: data) {
      throw mapped
    }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }

  // MARK: Profile probe (resume detection)

  // probeProfileExists hits GET /me (which sits behind RequireProfile) and
  // distinguishes 200 (profile complete) from 409 (profile_required).
  // Used by ProfileModel on app launch to decide whether to surface the
  // signup flow.
  static func probeProfileExists(auth: AuthModel) async throws -> Bool {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent("me"))
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }
    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    if code == 200 { return true }
    if code == 409,
      let body = try? JSONDecoder().decode([String: String].self, from: data),
      body["error"] == "profile_required"
    {
      return false
    }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }

  // MARK: error mapping

  private static func mapErrorBody(code: Int, data: Data) -> APIError? {
    guard let body = try? JSONDecoder().decode([String: String].self, from: data) else {
      return nil
    }
    switch (code, body["error"]) {
    case (409, "handle_taken"): return .handleTaken
    case (409, "profile_required"): return .profileRequired
    case (403, "avatar_path_not_owned"): return .avatarPathNotOwned
    case (422, "handle_format"): return .handleFormat
    case (422, "handle_display_mismatch"): return .handleDisplayMismatch
    case (422, "display_name_empty"): return .displayNameEmpty
    case (422, "dob_format"): return .dobFormat
    case (422, "dob_too_recent"): return .dobTooRecent
    case (422, "tos_version_mismatch"): return .tosVersionMismatch
    default: return nil
    }
  }
}
