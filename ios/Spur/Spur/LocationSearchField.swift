import CoreLocation
import MapKit
import SwiftUI

struct LocationSearchField: View {
  let region: MKCoordinateRegion
  let geocoder: GeocodingService
  let onSelect: (GeocodingResult) -> Void

  @State private var query: String = ""
  @State private var results: [GeocodingResult] = []
  @State private var searching: Bool = false

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack {
        Image(systemName: "magnifyingglass")
          .foregroundStyle(.secondary)
        TextField("Search a place", text: $query)
          .textInputAutocapitalization(.never)
          .autocorrectionDisabled()
          .accessibilityIdentifier("create.locationSearch")
        if !query.isEmpty {
          Button {
            query = ""
            results = []
          } label: {
            Image(systemName: "xmark.circle.fill")
              .foregroundStyle(.secondary)
          }
          .buttonStyle(.plain)
        }
      }
      .padding(.vertical, 6)
      .padding(.horizontal, 10)
      .background(Color(.secondarySystemBackground))
      .clipShape(RoundedRectangle(cornerRadius: 8))

      if !results.isEmpty {
        VStack(spacing: 0) {
          ForEach(results) { result in
            Button {
              onSelect(result)
              results = []
            } label: {
              HStack {
                VStack(alignment: .leading, spacing: 2) {
                  Text(result.title)
                    .font(.subheadline)
                    .foregroundStyle(.primary)
                  if !result.subtitle.isEmpty {
                    Text(result.subtitle)
                      .font(.caption)
                      .foregroundStyle(.secondary)
                  }
                }
                Spacer()
              }
              .contentShape(Rectangle())
              .padding(.vertical, 6)
              .padding(.horizontal, 10)
            }
            .buttonStyle(.plain)
            if result.id != results.last?.id {
              Divider().padding(.leading, 10)
            }
          }
        }
        .background(Color(.secondarySystemBackground))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .accessibilityIdentifier("create.locationResults")
      }
    }
    .task(id: query) {
      await debouncedSearch(query)
    }
  }

  private func debouncedSearch(_ q: String) async {
    let trimmed = q.trimmingCharacters(in: .whitespacesAndNewlines)
    guard trimmed.count >= 2 else {
      results = []
      return
    }
    do {
      try await Task.sleep(nanoseconds: 350_000_000)
    } catch {
      return
    }
    searching = true
    let next = await geocoder.search(query: trimmed, region: region)
    if Task.isCancelled { return }
    results = next
    searching = false
  }
}
