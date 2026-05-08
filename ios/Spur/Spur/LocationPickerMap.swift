import CoreLocation
import MapLibre
import SwiftUI
import UIKit

// Tap-anywhere map for choosing the true pin of a new Event. The single pin
// follows whichever point the user last tapped. The parent owns the
// coordinate via a Binding so the create-event form can read it back at
// submit time.
struct LocationPickerMap: UIViewRepresentable {
  let styleURL: URL
  let initialCenter: CLLocationCoordinate2D
  @Binding var coordinate: CLLocationCoordinate2D
  var recenterTrigger: Int = 0

  func makeCoordinator() -> Coordinator {
    Coordinator(coordinate: $coordinate)
  }

  func makeUIView(context: Context) -> MLNMapView {
    let mapView = MLNMapView(frame: .zero, styleURL: styleURL)
    mapView.delegate = context.coordinator
    mapView.logoView.isHidden = true
    mapView.attributionButton.isHidden = true
    mapView.minimumPitch = 0
    mapView.maximumPitch = 0
    mapView.setCenter(initialCenter, zoomLevel: 14, animated: false)

    let tap = UITapGestureRecognizer(
      target: context.coordinator, action: #selector(Coordinator.handleTap(_:)))
    tap.delegate = context.coordinator
    mapView.addGestureRecognizer(tap)
    context.coordinator.attach(mapView: mapView)
    context.coordinator.placeAnnotation(at: coordinate, on: mapView)
    return mapView
  }

  func updateUIView(_ uiView: MLNMapView, context: Context) {
    context.coordinator.placeAnnotation(at: coordinate, on: uiView)
    if context.coordinator.lastRecenterTrigger != recenterTrigger {
      context.coordinator.lastRecenterTrigger = recenterTrigger
      uiView.setCenter(coordinate, zoomLevel: uiView.zoomLevel, animated: true)
    }
  }

  final class Coordinator: NSObject, MLNMapViewDelegate, UIGestureRecognizerDelegate {
    private var coordinateBinding: Binding<CLLocationCoordinate2D>
    private weak var mapView: MLNMapView?
    private var annotation: PickAnnotation?
    var lastRecenterTrigger: Int = 0

    init(coordinate: Binding<CLLocationCoordinate2D>) {
      self.coordinateBinding = coordinate
    }

    func attach(mapView: MLNMapView) {
      self.mapView = mapView
    }

    func placeAnnotation(at coord: CLLocationCoordinate2D, on mapView: MLNMapView) {
      if let existing = annotation,
        existing.coordinate.latitude == coord.latitude,
        existing.coordinate.longitude == coord.longitude
      {
        return
      }
      if let existing = annotation {
        mapView.removeAnnotation(existing)
      }
      let next = PickAnnotation(coordinate: coord)
      annotation = next
      mapView.addAnnotation(next)
    }

    @objc func handleTap(_ recognizer: UITapGestureRecognizer) {
      guard let mapView else { return }
      let point = recognizer.location(in: mapView)
      let coord = mapView.convert(point, toCoordinateFrom: mapView)
      coordinateBinding.wrappedValue = coord
      placeAnnotation(at: coord, on: mapView)
    }

    // Allow our tap to coexist with MapLibre's own gestures.
    func gestureRecognizer(
      _ gestureRecognizer: UIGestureRecognizer,
      shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer
    ) -> Bool {
      true
    }

    func mapView(_ mapView: MLNMapView, imageFor annotation: MLNAnnotation) -> MLNAnnotationImage? {
      let reuseID = "spur-pick-pin"
      if let existing = mapView.dequeueReusableAnnotationImage(withIdentifier: reuseID) {
        return existing
      }
      return MLNAnnotationImage(image: Self.pinImage, reuseIdentifier: reuseID)
    }

    func mapView(_ mapView: MLNMapView, annotationCanShowCallout annotation: MLNAnnotation) -> Bool
    {
      false
    }

    private static let pinImage: UIImage = {
      let diameter: CGFloat = 32
      let renderer = UIGraphicsImageRenderer(
        size: CGSize(width: diameter, height: diameter))
      return renderer.image { ctx in
        let rect = CGRect(x: 0, y: 0, width: diameter, height: diameter)
        ctx.cgContext.setShadow(
          offset: CGSize(width: 0, height: 1), blur: 3,
          color: UIColor.black.withAlphaComponent(0.3).cgColor)
        UIColor.systemBlue.setFill()
        UIBezierPath(ovalIn: rect.insetBy(dx: 2, dy: 2)).fill()
        ctx.cgContext.setShadow(offset: .zero, blur: 0, color: nil)
        UIColor.white.setStroke()
        let ring = UIBezierPath(ovalIn: rect.insetBy(dx: 2, dy: 2))
        ring.lineWidth = 2
        ring.stroke()
      }
    }()
  }

  final class PickAnnotation: NSObject, MLNAnnotation {
    var coordinate: CLLocationCoordinate2D
    init(coordinate: CLLocationCoordinate2D) {
      self.coordinate = coordinate
    }
  }
}
