import Foundation
import Observation
import Supabase

@MainActor
@Observable
final class AuthModel {
  enum Phase: Equatable {
    case loading
    case signedOut
    case awaitingCode(phone: String)
    case signedIn(userID: UUID)
  }

  var phase: Phase = .loading
  var lastError: String?

  private let client: SupabaseClient

  init(client: SupabaseClient = SupabaseConfig.shared) {
    self.client = client
    Task { await observe() }
  }

  private func observe() async {
    for await (_, session) in client.auth.authStateChanges {
      apply(session: session)
    }
  }

  private func apply(session: Session?) {
    if let session {
      phase = .signedIn(userID: session.user.id)
      return
    }
    if case .awaitingCode = phase { return }
    phase = .signedOut
  }

  func sendCode(phone: String) async {
    lastError = nil
    do {
      try await client.auth.signInWithOTP(phone: phone)
      phase = .awaitingCode(phone: phone)
    } catch {
      lastError = error.localizedDescription
    }
  }

  func verify(code: String) async {
    guard case .awaitingCode(let phone) = phase else { return }
    lastError = nil
    do {
      try await client.auth.verifyOTP(phone: phone, token: code, type: .sms)
    } catch {
      lastError = error.localizedDescription
    }
  }

  func signOut() async {
    do {
      try await client.auth.signOut()
    } catch {
      lastError = error.localizedDescription
    }
  }

  func cancelCodeEntry() {
    if case .awaitingCode = phase {
      phase = .signedOut
      lastError = nil
    }
  }

  func accessToken() async -> String? {
    try? await client.auth.session.accessToken
  }

  var userID: UUID? {
    if case .signedIn(let id) = phase { return id }
    return nil
  }
}
