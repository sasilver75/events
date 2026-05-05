import SwiftUI

struct CountryPicker: View {
  @Binding var selection: Country
  @Environment(\.dismiss) private var dismiss
  @State private var query: String = ""

  private var matches: [Country] {
    let trimmed = query.trimmingCharacters(in: .whitespaces)
    guard !trimmed.isEmpty else { return Country.all }
    let lower = trimmed.lowercased()
    let stripped = lower.replacingOccurrences(of: "+", with: "")
    return Country.all.filter { c in
      c.name.lowercased().contains(lower)
        || c.id.lowercased().contains(lower)
        || c.dialCode.replacingOccurrences(of: "+", with: "").contains(stripped)
    }
  }

  var body: some View {
    NavigationStack {
      List(matches) { country in
        Button {
          selection = country
          dismiss()
        } label: {
          HStack {
            Text(country.flag)
            Text(country.name)
              .foregroundStyle(.primary)
            Spacer()
            Text(country.dialCode)
              .foregroundStyle(.secondary)
              .monospacedDigit()
            if country == selection {
              Image(systemName: "checkmark")
                .foregroundStyle(.tint)
            }
          }
        }
      }
      .searchable(text: $query, prompt: "Country or dial code")
      .navigationTitle("Country")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) {
          Button("Cancel") { dismiss() }
        }
      }
    }
  }
}
