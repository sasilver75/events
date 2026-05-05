import CoreLocation
import MapLibre
import SwiftUI

struct MapView: UIViewRepresentable {
  let styleURL: URL
  let center: CLLocationCoordinate2D
  let zoom: Double
  var pitch: CGFloat = 45
  var heading: CLLocationDirection = 0

  func makeCoordinator() -> Coordinator { Coordinator() }

  func makeUIView(context: Context) -> MLNMapView {
    let mapView = MLNMapView(frame: .zero, styleURL: styleURL)
    mapView.delegate = context.coordinator
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

  final class Coordinator: NSObject, MLNMapViewDelegate {
    func mapView(_ mapView: MLNMapView, didFinishLoading style: MLNStyle) {
      guard let source = style.source(withIdentifier: "protomaps") as? MLNVectorTileSource else {
        return
      }
      let layer = MLNFillExtrusionStyleLayer(identifier: "buildings_3d", source: source)
      layer.sourceLayerIdentifier = "buildings"
      layer.predicate = NSPredicate(format: "kind IN %@", ["building", "building_part"])
      layer.minimumZoomLevel = 14
      layer.fillExtrusionColor = NSExpression(
        forConstantValue: UIColor(white: 0xCC / 255.0, alpha: 1.0))
      layer.fillExtrusionHeight = NSExpression(format: "TERNARY(height == nil, 4, height)")
      layer.fillExtrusionBase = NSExpression(forConstantValue: 0)
      layer.fillExtrusionOpacity = NSExpression(forConstantValue: 0.85)
      if let firstSymbol = style.layers.first(where: { $0 is MLNSymbolStyleLayer }) {
        style.insertLayer(layer, below: firstSymbol)
      } else {
        style.addLayer(layer)
      }
    }
  }
}

extension Bundle {
  var mapStyleLightURL: URL {
    guard let url = url(forResource: "MapStyleLight", withExtension: "json") else {
      fatalError("MapStyleLight.json missing from app bundle")
    }
    return url
  }
}
