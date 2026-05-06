import CoreLocation

// One-shot best-accuracy location probe used at check-in tap time
// (PRD §At-event, ADR 0011). Distinct from `LocationManager`, which uses
// kCLLocationAccuracyHundredMeters for the Browse-fetch use case — the
// geofence rule reads the iOS-reported `horizontalAccuracy`, so we want
// the most honest fix CoreLocation can produce here.
//
// One probe per check-in tap: instantiate, await `requestFix()`, discard.
@MainActor
final class CheckInLocationProbe: NSObject, CLLocationManagerDelegate {
  private let manager = CLLocationManager()
  private var continuation: CheckedContinuation<CLLocation, Error>?

  enum ProbeError: LocalizedError {
    case notAuthorized

    var errorDescription: String? {
      switch self {
      case .notAuthorized: return "Enable Location Services to check in"
      }
    }
  }

  override init() {
    super.init()
    manager.delegate = self
    manager.desiredAccuracy = kCLLocationAccuracyBest
  }

  func requestFix() async throws -> CLLocation {
    let status = manager.authorizationStatus
    guard status == .authorizedWhenInUse || status == .authorizedAlways else {
      throw ProbeError.notAuthorized
    }
    return try await withCheckedThrowingContinuation { c in
      self.continuation = c
      manager.requestLocation()
    }
  }

  nonisolated func locationManager(
    _ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]
  ) {
    guard let loc = locations.last else { return }
    Task { @MainActor in
      let c = self.continuation
      self.continuation = nil
      c?.resume(returning: loc)
    }
  }

  nonisolated func locationManager(
    _ manager: CLLocationManager, didFailWithError error: Error
  ) {
    Task { @MainActor in
      let c = self.continuation
      self.continuation = nil
      c?.resume(throwing: error)
    }
  }
}
