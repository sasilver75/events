import Foundation
import Observation
import Supabase

@MainActor
@Observable
final class AuthModel {
  // SignupCompletion encodes the resume-into-signup state introduced by #88
  // / ADR 0025. After OTP verify, the iOS app probes /me. The server's
  // 409 profile_required / 409 avatar_required map directly to .needsProfile
  // / .needsAvatar so the client knows where to reopen the signup flow.
  enum SignupCompletion: Equatable {
    case unknown  // probe in flight
    case needsProfile  // POST /users/me/profile next
    case needsAvatar  // upload avatar + POST /users/me/avatar next
    case complete  // full app available
  }

  enum Phase: Equatable {
    case loading
    case signedOut
    case awaitingCode(phone: String)
    case signedIn(userID: UUID, signup: SignupCompletion)
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
      // Default to .unknown on first appearance; refreshSignupState fires
      // the probe and updates phase when the server replies.
      let userID = session.user.id
      if case .signedIn(let existing, _) = phase, existing == userID {
        // Already signed in as this user — leave the resolved completion
        // state alone (refreshSignupState may be in flight).
        return
      }
      phase = .signedIn(userID: userID, signup: .unknown)
      Task { await refreshSignupState() }
      return
    }
    if case .awaitingCode = phase { return }
    phase = .signedOut
  }

  // refreshSignupState calls the server's /me probe, which the
  // profile_required + avatar_required middleware uses to surface the
  // current resume step. Called automatically after sign-in and on demand
  // after the user advances through a signup screen.
  func refreshSignupState() async {
    guard case .signedIn(let userID, _) = phase else { return }
    do {
      let gate = try await UsersAPI.probeSignupState(auth: self)
      let completion: SignupCompletion = {
        switch gate {
        case .complete: return .complete
        case .needsProfile: return .needsProfile
        case .needsAvatar: return .needsAvatar
        }
      }()
      // Re-check phase in case the user signed out while we awaited.
      if case .signedIn(let stillUser, _) = phase, stillUser == userID {
        phase = .signedIn(userID: userID, signup: completion)
      }
    } catch {
      // Leave phase as-is; user can retry. Surface the message so the
      // signup screens can show "we couldn't reach the server."
      lastError =
        (error as? UsersAPI.APIError)?.errorDescription
        ?? error.localizedDescription
    }
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
    if case .signedIn(let id, _) = phase { return id }
    return nil
  }
}
