import Foundation

// MARK: - ChatAPI
//
// Client for the per-Event chat endpoints (#65, ADR 0006):
//   POST /events/:id/messages         — send a user message
//   GET  /events/:id/messages         — history (cursor pagination)
//   GET  /events/:id/messages/stream  — SSE live + Last-Event-ID replay
//
// The stream API exposes AsyncThrowingStream<Message, Error> so SwiftUI can
// drive a `for try await` loop in a Task and cancel cleanly on view dismiss.
enum ChatAPI {
  enum APIError: LocalizedError {
    case noToken
    case http(Int, String)
    case transport(String)
    case decode(String)
    case chatLocked

    var errorDescription: String? {
      switch self {
      case .noToken: return "no access token"
      case .http(let code, let body): return "HTTP \(code): \(body)"
      case .transport(let s): return s
      case .decode(let s): return "decode: \(s)"
      case .chatLocked: return "Chat opens when this event Tips."
      }
    }
  }

  struct Message: Decodable, Identifiable, Hashable {
    let id: Int64
    let eventID: String
    let senderID: String?
    let body: String
    let sentAt: Date
    let kind: String  // "user" | "system"

    enum CodingKeys: String, CodingKey {
      case id
      case eventID = "event_id"
      case senderID = "sender_id"
      case body
      case sentAt = "sent_at"
      case kind
    }
  }

  static func fetchHistory(eventID: String, since: Int64 = 0, auth: AuthModel) async throws
    -> [Message]
  {
    var components = URLComponents(
      url: SupabaseConfig.serverURL.appendingPathComponent("events/\(eventID)/messages"),
      resolvingAgainstBaseURL: false
    )!
    if since > 0 {
      components.queryItems = [URLQueryItem(name: "since", value: String(since))]
    }
    guard let token = await auth.accessToken() else { throw APIError.noToken }
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
      return try decoder.decode([Message].self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  static func sendMessage(eventID: String, body: String, auth: AuthModel) async throws -> Message {
    struct SendBody: Encodable { let body: String }
    guard let token = await auth.accessToken() else { throw APIError.noToken }
    var req = URLRequest(
      url: SupabaseConfig.serverURL.appendingPathComponent("events/\(eventID)/messages"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    do {
      req.httpBody = try JSONEncoder().encode(SendBody(body: body))
    } catch {
      throw APIError.transport("encode: \(error.localizedDescription)")
    }
    let data: Data
    let resp: URLResponse
    do {
      (data, resp) = try await URLSession.shared.data(for: req)
    } catch {
      throw APIError.transport(error.localizedDescription)
    }
    let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
    if code == 409, let err = try? JSONDecoder().decode([String: String].self, from: data),
      err["error"] == "chat_locked"
    {
      throw APIError.chatLocked
    }
    guard code == 201 else {
      throw APIError.http(code, String(data: data, encoding: .utf8) ?? "")
    }
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601withFractionalSeconds
    do {
      return try decoder.decode(Message.self, from: data)
    } catch {
      throw APIError.decode(error.localizedDescription)
    }
  }

  // openStream returns an AsyncThrowingStream of Messages emitted by the
  // server's SSE endpoint. Pass `lastEventID` (typically the last id the
  // caller has rendered) to request a replay of missed messages on
  // reconnect; the server uses the same monotonic id cursor as
  // fetchHistory's `since` parameter.
  //
  // The stream is finished when (a) the caller cancels the consuming Task,
  // (b) the connection closes, or (c) a fatal parse error occurs. SSE
  // heartbeat comments (`: keepalive`) are silently skipped.
  static func openStream(
    eventID: String,
    lastEventID: Int64?,
    auth: AuthModel
  ) -> AsyncThrowingStream<Message, Error> {
    AsyncThrowingStream { continuation in
      let task = Task { [continuation] in
        do {
          guard let token = await auth.accessToken() else { throw APIError.noToken }
          var req = URLRequest(
            url: SupabaseConfig.serverURL.appendingPathComponent(
              "events/\(eventID)/messages/stream"))
          req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
          req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
          if let id = lastEventID, id > 0 {
            req.setValue(String(id), forHTTPHeaderField: "Last-Event-ID")
          }
          // No URLSession timeout — SSE streams are intentionally long-lived
          // while the chat sheet is visible. The consumer's Task cancellation
          // is the canonical "stop reading" signal.
          req.timeoutInterval = .infinity

          let (bytes, resp) = try await URLSession.shared.bytes(for: req)
          let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
          guard code == 200 else {
            // Drain a small head for context in the error string.
            var headBytes = Data()
            for try await b in bytes {
              headBytes.append(b)
              if headBytes.count >= 256 { break }
            }
            throw APIError.http(code, String(data: headBytes, encoding: .utf8) ?? "")
          }

          let decoder = JSONDecoder()
          decoder.dateDecodingStrategy = .iso8601withFractionalSeconds

          var dataAccum = ""
          for try await line in bytes.lines {
            try Task.checkCancellation()
            if line.isEmpty {
              if !dataAccum.isEmpty {
                if let body = dataAccum.data(using: .utf8),
                  let msg = try? decoder.decode(Message.self, from: body)
                {
                  continuation.yield(msg)
                }
                dataAccum = ""
              }
              continue
            }
            if line.hasPrefix(":") {
              continue  // heartbeat
            }
            if line.hasPrefix("data:") {
              // SSE allows a single optional space after the colon.
              let after = line.index(line.startIndex, offsetBy: 5)
              var payload = String(line[after...])
              if payload.hasPrefix(" ") { payload.removeFirst() }
              dataAccum.append(payload)
            }
            // id: lines are tracked by URLSession via Last-Event-ID but
            // we don't need to surface them up.
          }
          continuation.finish()
        } catch is CancellationError {
          continuation.finish()
        } catch {
          continuation.finish(throwing: error)
        }
      }
      continuation.onTermination = { _ in task.cancel() }
    }
  }
}
