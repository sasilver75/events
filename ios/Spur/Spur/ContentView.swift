import CoreLocation
import SwiftUI

struct ContentView: View {
  private let losAngeles = CLLocationCoordinate2D(latitude: 34.0522, longitude: -118.2437)

  var body: some View {
    ZStack(alignment: .top) {
      MapView(
        styleURL: Bundle.main.mapStyleLightURL,
        center: losAngeles,
        zoom: 11
      )
      .ignoresSafeArea()

      Text("Spur — Los Angeles")
        .font(.title2)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(.regularMaterial, in: Capsule())
        .padding(.top, 8)
    }
  }
}

#Preview {
  ContentView()
}
