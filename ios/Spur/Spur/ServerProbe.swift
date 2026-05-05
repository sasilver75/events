import Foundation

enum ServerProbe {
  struct MeResponse: Decodable {
    let userID: String
    enum CodingKeys: String, CodingKey { case userID = "user_id" }
  }

  enum ProbeError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      }
    }
  }

  static func me(auth: AuthModel) async throws -> String {
    guard let token = await auth.accessToken() else { throw ProbeError.noToken }
    var req = URLRequest(url: SupabaseConfig.serverURL.appendingPathComponent("me"))
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      let (data, resp) = try await URLSession.shared.data(for: req)
      let http = resp as? HTTPURLResponse
      let code = http?.statusCode ?? -1
      guard code == 200 else {
        throw ProbeError.http(code, String(data: data, encoding: .utf8) ?? "")
      }
      return try JSONDecoder().decode(MeResponse.self, from: data).userID
    } catch let e as ProbeError {
      throw e
    } catch {
      throw ProbeError.transport(error.localizedDescription)
    }
  }
}
