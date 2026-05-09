import Foundation
import Observation

// ProfileModel tracks whether the authenticated user has completed the
// signup-completeness flow (#88, ADR 0025). It probes GET /me on each
// transition into AuthModel.signedIn — 200 means the public.users row
// exists and the user can land on the map; 409 profile_required means
// the row is missing and the iOS app must surface ProfileSetupView.
//
// The phase split mirrors the AuthModel pattern: a `.loading`-style
// `.unknown` state covers the gap between sign-in and the probe's
// completion so RootView shows a spinner instead of flashing the wrong
// screen.
@MainActor
@Observable
final class ProfileModel {
  enum Phase: Equatable {
    case unknown
    case probing
    case needed
    case complete
    case error(String)
  }

  var phase: Phase = .unknown

  // Idempotent. Call from the view that owns the auth → profile transition
  // (RootView .task). Re-entering while already in `.probing` short-circuits
  // so a re-render doesn't fire two parallel probes.
  func probe(auth: AuthModel) async {
    if phase == .probing { return }
    phase = .probing
    do {
      let exists = try await UsersAPI.probeProfileExists(auth: auth)
      phase = exists ? .complete : .needed
    } catch {
      phase = .error(error.localizedDescription)
    }
  }

  // Called by ProfileSetupView after a successful profile + avatar submit
  // so RootView swaps to the map without an extra round-trip.
  func markComplete() {
    phase = .complete
  }

  // Called on sign-out so the next sign-in re-probes from scratch.
  func reset() {
    phase = .unknown
  }
}
