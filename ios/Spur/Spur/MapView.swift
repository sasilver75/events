import CoreLocation
import MapLibre
import SwiftUI
import UIKit

struct MapView: UIViewRepresentable {
  let styleURL: URL
  let center: CLLocationCoordinate2D
  let zoom: Double
  var pitch: CGFloat = 45
  var heading: CLLocationDirection = 0
  var events: [NearbyEvent] = []
  var onSelect: (NearbyEvent) -> Void = { _ in }
  var centerBinding: Binding<CLLocationCoordinate2D>?

  func makeCoordinator() -> Coordinator {
    Coordinator(onSelect: onSelect, centerBinding: centerBinding)
  }

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

  func updateUIView(_ uiView: MLNMapView, context: Context) {
    context.coordinator.onSelect = onSelect
    context.coordinator.centerBinding = centerBinding
    context.coordinator.sync(events: events, on: uiView)
  }

  // EventAnnotation carries the full Event payload alongside its coordinate
  // so the didSelect delegate callback can hand a typed value back to SwiftUI
  // without a sidecar lookup table.
  final class EventAnnotation: NSObject, MLNAnnotation {
    let event: NearbyEvent
    var coordinate: CLLocationCoordinate2D { event.coordinate }
    var title: String? { event.title }
    init(event: NearbyEvent) {
      self.event = event
      super.init()
    }
  }

  final class Coordinator: NSObject, MLNMapViewDelegate {
    var onSelect: (NearbyEvent) -> Void
    var centerBinding: Binding<CLLocationCoordinate2D>?
    private var byID: [String: EventAnnotation] = [:]

    init(
      onSelect: @escaping (NearbyEvent) -> Void,
      centerBinding: Binding<CLLocationCoordinate2D>?
    ) {
      self.onSelect = onSelect
      self.centerBinding = centerBinding
    }

    func sync(events: [NearbyEvent], on mapView: MLNMapView) {
      let nextByID = Dictionary(uniqueKeysWithValues: events.map { ($0.id, $0) })

      // Treat any payload diff as remove + re-add so coordinate, category,
      // and commit-state changes all propagate to MapLibre. The cached
      // EventAnnotation otherwise holds a stale NearbyEvent and didSelect
      // hands an out-of-date copy back to SwiftUI.
      var stale: [EventAnnotation] = []
      for (id, annotation) in byID where nextByID[id] != annotation.event {
        stale.append(annotation)
      }
      if !stale.isEmpty {
        mapView.removeAnnotations(stale.map { $0 as MLNAnnotation })
        for a in stale { byID.removeValue(forKey: a.event.id) }
      }

      var toAdd: [EventAnnotation] = []
      for e in events where byID[e.id] == nil {
        let a = EventAnnotation(event: e)
        byID[e.id] = a
        toAdd.append(a)
      }
      if !toAdd.isEmpty {
        mapView.addAnnotations(toAdd)
      }
    }

    // MARK: MLNMapViewDelegate

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

    func mapView(_ mapView: MLNMapView, imageFor annotation: MLNAnnotation) -> MLNAnnotationImage? {
      guard let event = (annotation as? EventAnnotation)?.event else { return nil }
      let category = event.categoryEnum
      let reuseID = "spur-pin-\(category.rawValue)"
      if let existing = mapView.dequeueReusableAnnotationImage(withIdentifier: reuseID) {
        return existing
      }
      let image = Self.pinImage(for: category)
      return MLNAnnotationImage(image: image, reuseIdentifier: reuseID)
    }

    func mapView(_ mapView: MLNMapView, annotationCanShowCallout annotation: MLNAnnotation) -> Bool
    {
      // Suppress the default callout balloon — taps go straight to the
      // SwiftUI detail sheet via didSelect below.
      false
    }

    func mapView(_ mapView: MLNMapView, didSelect annotation: MLNAnnotation) {
      guard let event = (annotation as? EventAnnotation)?.event else { return }
      onSelect(event)
      mapView.deselectAnnotation(annotation, animated: false)
    }

    func mapView(_ mapView: MLNMapView, regionDidChangeAnimated animated: Bool) {
      centerBinding?.wrappedValue = mapView.centerCoordinate
    }

    // pinImage renders a category-tinted circle with the SF Symbol icon
    // centered on top, suitable for use as an MLNAnnotationImage. Drawn at
    // 3x for retina; MapLibre treats the image's size as logical points.
    private static func pinImage(for category: EventCategory) -> UIImage {
      let diameter: CGFloat = 36
      let size = CGSize(width: diameter, height: diameter)

      let renderer = UIGraphicsImageRenderer(size: size)

      return renderer.image { ctx in
        let rect = CGRect(origin: .zero, size: size)

        // Drop shadow.
        ctx.cgContext.setShadow(
          offset: CGSize(width: 0, height: 1), blur: 3,
          color: UIColor.black.withAlphaComponent(0.3).cgColor)

        // Filled circle in the category color.
        UIColor(category.color).setFill()
        UIBezierPath(ovalIn: rect.insetBy(dx: 2, dy: 2)).fill()

        ctx.cgContext.setShadow(offset: .zero, blur: 0, color: nil)

        // White ring outline.
        UIColor.white.setStroke()
        let ring = UIBezierPath(ovalIn: rect.insetBy(dx: 2, dy: 2))
        ring.lineWidth = 2
        ring.stroke()

        // Centered SF Symbol glyph.
        let config = UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)
        if let glyph = UIImage(systemName: category.symbolName, withConfiguration: config)?
          .withTintColor(.white, renderingMode: .alwaysOriginal)
        {
          let glyphSize = glyph.size
          let glyphRect = CGRect(
            x: (diameter - glyphSize.width) / 2,
            y: (diameter - glyphSize.height) / 2,
            width: glyphSize.width,
            height: glyphSize.height)
          glyph.draw(in: glyphRect)
        }
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
