import CoreLocation
import Foundation
import MapKit

struct GeocodingResult: Identifiable, Hashable {
  let id = UUID()
  let title: String
  let subtitle: String
  let coordinate: CLLocationCoordinate2D

  static func == (lhs: GeocodingResult, rhs: GeocodingResult) -> Bool { lhs.id == rhs.id }
  func hash(into hasher: inout Hasher) { hasher.combine(id) }
}

protocol GeocodingService: Sendable {
  func search(query: String, region: MKCoordinateRegion) async -> [GeocodingResult]
  func reverseGeocode(_ coord: CLLocationCoordinate2D) async -> String?
}

struct AppleGeocodingService: GeocodingService {
  func search(query: String, region: MKCoordinateRegion) async -> [GeocodingResult] {
    let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return [] }

    let request = MKLocalSearch.Request()
    request.naturalLanguageQuery = trimmed
    request.region = region
    request.resultTypes = [.pointOfInterest, .address]

    do {
      let response = try await MKLocalSearch(request: request).start()
      return response.mapItems.prefix(8).map { item in
        GeocodingResult(
          title: item.name ?? trimmed,
          subtitle: item.addressRepresentations?.cityWithContext ?? "",
          coordinate: item.location.coordinate)
      }
    } catch {
      return []
    }
  }

  func reverseGeocode(_ coord: CLLocationCoordinate2D) async -> String? {
    let location = CLLocation(latitude: coord.latitude, longitude: coord.longitude)
    guard let request = MKReverseGeocodingRequest(location: location) else { return nil }
    do {
      let items = try await request.mapItems
      guard let item = items.first else { return nil }
      let name = item.name ?? ""
      let city = item.addressRepresentations?.cityName ?? ""
      if name.isEmpty && city.isEmpty { return nil }
      if city.isEmpty { return name }
      if name.isEmpty { return city }
      if name == city { return name }
      return "\(name) · \(city)"
    } catch {
      return nil
    }
  }
}
