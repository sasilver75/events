import CoreLocation
import MapLibre
import SwiftUI

struct MapView: UIViewRepresentable {
  let styleURL: URL
  let center: CLLocationCoordinate2D
  let zoom: Double
  var pitch: CGFloat = 45
  var heading: CLLocationDirection = 0

  func makeUIView(context: Context) -> MLNMapView {
    let mapView = MLNMapView(frame: .zero, styleURL: styleURL)
    mapView.logoView.isHidden = true
    mapView.attributionButton.isHidden = true
    mapView.minimumPitch = 0
    mapView.maximumPitch = 75
    mapView.setCenter(center, zoomLevel: zoom, animated: false)
    let camera = mapView.camera
    camera.pitch = pitch
    camera.heading = heading
    mapView.camera = camera
    return mapView
  }

  func updateUIView(_ uiView: MLNMapView, context: Context) {}
}

extension Bundle {
  var mapStyleLightURL: URL {
    guard let url = url(forResource: "MapStyleLight", withExtension: "json") else {
      fatalError("MapStyleLight.json missing from app bundle")
    }
    return url
  }
}
