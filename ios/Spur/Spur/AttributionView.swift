import SafariServices
import SwiftUI

struct AttributionView: View {
  @Environment(\.dismiss) private var dismiss
  @State private var presentedLink: PresentedLink?

  private let credits: [Credit] = [
    Credit(
      text: "Map data © OpenStreetMap contributors",
      url: URL(string: "https://www.openstreetmap.org/copyright")!
    ),
    Credit(
      text: "Tiles © Protomaps",
      url: URL(string: "https://protomaps.com")!
    ),
    Credit(
      text: "Renderer: MapLibre Native (BSD-2)",
      url: URL(string: "https://maplibre.org")!
    ),
  ]

  var body: some View {
    NavigationStack {
      List {
        Section {
          ForEach(credits) { credit in
            Button {
              presentedLink = PresentedLink(url: credit.url)
            } label: {
              HStack {
                Text(credit.text)
                  .foregroundStyle(.primary)
                  .multilineTextAlignment(.leading)
                Spacer(minLength: 12)
                Image(systemName: "arrow.up.right.square")
                  .foregroundStyle(.secondary)
              }
            }
            .accessibilityHint("Opens \(credit.url.host() ?? credit.url.absoluteString)")
          }
        } header: {
          Text("Map credits")
        } footer: {
          Text(
            "Spur renders OpenStreetMap data via Protomaps vector tiles, "
              + "drawn by MapLibre Native."
          )
        }
      }
      .navigationTitle("Attribution")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .confirmationAction) {
          Button("Done") { dismiss() }
        }
      }
      .sheet(item: $presentedLink) { link in
        SafariView(url: link.url)
          .ignoresSafeArea()
      }
    }
  }
}

private struct Credit: Identifiable {
  let text: String
  let url: URL
  var id: URL { url }
}

private struct PresentedLink: Identifiable {
  let url: URL
  var id: URL { url }
}

private struct SafariView: UIViewControllerRepresentable {
  let url: URL

  func makeUIViewController(context: Context) -> SFSafariViewController {
    SFSafariViewController(url: url)
  }

  func updateUIViewController(_ uiViewController: SFSafariViewController, context: Context) {}
}

#Preview {
  AttributionView()
}
