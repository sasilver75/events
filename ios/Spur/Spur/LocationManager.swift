import CoreLocation
import Observation

// Thin Observable wrapper around CLLocationManager for the Browse flow.
//
// Per PRD-v0:165 we are one-shot: ask once on first map appearance, never
// re-prompt in v0. Callers read `authorizationStatus` to decide whether to
// show the value-prop screen, then call `requestWhenInUseAuthorization()` on
// CTA. Granted → single `requestLocation()` for a fix; denied → callers fall
// back to a hardcoded center.
@MainActor
@Observable
final class LocationManager: NSObject, CLLocationManagerDelegate {
  var authorizationStatus: CLAuthorizationStatus
  var lastFix: CLLocationCoordinate2D?
  var lastError: String?

  private let manager = CLLocationManager()

  override init() {
    self.authorizationStatus = manager.authorizationStatus
    super.init()
    manager.delegate = self
    manager.desiredAccuracy = kCLLocationAccuracyHundredMeters
  }

  func requestWhenInUseAuthorization() {
    manager.requestWhenInUseAuthorization()
  }

  func requestLocation() {
    manager.requestLocation()
  }

  // MARK: CLLocationManagerDelegate

  nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
    let status = manager.authorizationStatus
    Task { @MainActor in
      self.authorizationStatus = status
    }
  }

  nonisolated func locationManager(
    _ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]
  ) {
    guard let coord = locations.last?.coordinate else { return }
    Task { @MainActor in
      self.lastFix = coord
    }
  }

  nonisolated func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
    let msg = error.localizedDescription
    Task { @MainActor in
      self.lastError = msg
    }
  }
}
