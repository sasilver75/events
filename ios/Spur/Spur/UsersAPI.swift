import Foundation
import Supabase

// Profile-completion endpoints introduced by #88.
//
// The flow is: phone-OTP signup → POST /users/me/profile → upload avatar to
// Supabase Storage → POST /users/me/avatar. Until both calls land, any
// authenticated request to a profile_required or avatar_required route
// returns 409 with one of the codes below — the client maps that to the
// resume step.
enum UsersAPI {
  enum APIError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case profileComplete  // 409 from POST /users/me/profile when row exists
    case handleTaken  // 409 from POST /users/me/profile or HEAD probe
    case profileRequired  // 409 from any auth route gated by profile_required
    case avatarRequired  // 409 from any auth route gated by avatar_required
    case validation(String)

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let c, let b): return "HTTP \(c): \(b)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .profileComplete: return "Profile already created."
      case .handleTaken: return "That handle is taken."
      case .profileRequired: return "Profile setup needed."
      case .avatarRequired: return "Avatar required."
      case .validation(let m): return m
      }
    }
  }

  struct TOSDocument: Decodable {
    let version: String
    let content: String
  }

  static func fetchTOS() async throws -> TOSDocument {
    let url = SupabaseConfig.serverURL.appendingPathComponent("legal/tos")
    let req = URLRequest(url: url)
    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }
    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    guard code == 200 else { throw APIError.http(code, String(data: data, encoding: .utf8) ?? "") }
    do {
      return try JSONDecoder().decode(TOSDocument.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  // checkHandleAvailable issues HEAD /users/handle/{handle}.
  // Returns true when the handle is available (200), false when taken (409).
  // Throws APIError.validation on bad-format probes (400).
  static func checkHandleAvailable(_ handle: String, auth: AuthModel) async throws -> Bool {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    let url = SupabaseConfig.serverURL
      .appendingPathComponent("users/handle")
      .appendingPathComponent(handle)
    var req = URLRequest(url: url)
    req.httpMethod = "HEAD"
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let resp: URLResponse
    do {
      (_, resp) = try await URLSession.shared.data(for: req)
    } catch let urlErr as URLError where urlErr.code == .cancelled {
      throw CancellationError()
    } catch {
      throw APIError.transport(error.localizedDescription)
    }
    switch (resp as? HTTPURLResponse)?.statusCode ?? -1 {
    case 200: return true
    case 409: return false
    case 400: throw APIError.validation("handle format is invalid")
    case let code: throw APIError.http(code, "")
    }
  }

  struct ProfileSubmission: Encodable {
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

  // submitProfile POSTs /users/me/profile.
  //   - 201 on first successful create.
  //   - 409 profile_complete is mapped to APIError.profileComplete; the
  //     caller treats that as "advance" (the profile already exists).
  //   - 409 handle_taken is mapped to APIError.handleTaken so the form can
  //     surface the field-level error.
  static func submitProfile(_ submission: ProfileSubmission, auth: AuthModel) async throws {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent("users/me/profile"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      req.httpBody = try JSONEncoder().encode(submission)
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
    if code == 201 { return }
    if code == 409 {
      switch coded(data) {
      case "profile_complete": throw APIError.profileComplete
      case "handle_taken": throw APIError.handleTaken
      default: break
      }
    }
    if code == 422 {
      throw APIError.validation(messageFromBody(data))
    }
    throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
  }

  // submitAvatar POSTs /users/me/avatar with the storage object key the
  // client just uploaded under the per-user prefix.
  static func submitAvatar(path: String, auth: AuthModel) async throws {
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent("users/me/avatar"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      req.httpBody = try JSONEncoder().encode(["avatar_path": path])
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
    switch code {
    case 200: return
    case 409 where coded(data) == "profile_required": throw APIError.profileRequired
    default:
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
  }

  // Probes /me to discover whether the caller has completed signup. The
  // result feeds AuthModel's signup-state routing — see Phase.signedIn.
  enum SignupGate {
    case complete
    case needsProfile
    case needsAvatar
  }

  static func probeSignupState(auth: AuthModel) async throws -> SignupGate {
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
    switch code {
    case 200: return .complete
    case 409:
      switch coded(data) {
      case "profile_required": return .needsProfile
      case "avatar_required": return .needsAvatar
      default: throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
      }
    default:
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
  }

  private static func coded(_ data: Data) -> String? {
    struct Body: Decodable { let error: String? }
    return (try? JSONDecoder().decode(Body.self, from: data))?.error
  }

  private static func messageFromBody(_ data: Data) -> String {
    struct Body: Decodable {
      let error: String?
      let message: String?
    }
    let body = try? JSONDecoder().decode(Body.self, from: data)
    return body?.message ?? body?.error ?? String(data: data, encoding: .utf8)
      ?? "validation failed"
  }
}
