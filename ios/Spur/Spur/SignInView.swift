import SwiftUI

struct SignInView: View {
  @Environment(AuthModel.self) private var auth
  @State private var country: Country = .default
  @State private var phoneDigits: String = ""
  @State private var code: String = ""
  @State private var sending: Bool = false
  @State private var pickerOpen: Bool = false

  var body: some View {
    NavigationStack {
      Form {
        switch auth.phase {
        case .awaitingCode(let phone):
          codeSection(phone: phone)
        default:
          phoneSection
        }

        if let error = auth.lastError {
          Section {
            Text(error)
              .foregroundStyle(.red)
              .font(.footnote)
          }
        }
      }
      .navigationTitle("Sign in")
      .sheet(isPresented: $pickerOpen) {
        CountryPicker(selection: $country)
      }
    }
  }

  @ViewBuilder
  private var phoneSection: some View {
    Section {
      HStack {
        Button {
          pickerOpen = true
        } label: {
          HStack(spacing: 4) {
            Text(country.flag)
            Text(country.dialCode)
              .monospacedDigit()
              .foregroundStyle(.primary)
            Image(systemName: "chevron.down")
              .font(.caption2)
              .foregroundStyle(.secondary)
          }
        }
        .buttonStyle(.plain)

        Divider().frame(height: 22)

        TextField("Phone number", text: $phoneDigits)
          .keyboardType(.numberPad)
          .textContentType(.telephoneNumber)
          .autocorrectionDisabled()
          .onChange(of: phoneDigits) { _, new in
            let digits = new.filter(\.isNumber)
            if digits != new { phoneDigits = digits }
          }
      }

      Button(sending ? "Sending…" : "Send code") {
        Task {
          sending = true
          await auth.sendCode(phone: e164)
          sending = false
        }
      }
      .disabled(phoneDigits.isEmpty || sending)
    } header: {
      Text("Phone")
    } footer: {
      Text("Local dev: 🇺🇸 +1 4152127777, code 123456.")
    }
  }

  @ViewBuilder
  private func codeSection(phone: String) -> some View {
    Section("Code") {
      Text("Sent to \(phone)")
        .font(.footnote)
        .foregroundStyle(.secondary)
      TextField("6-digit code", text: $code)
        .keyboardType(.numberPad)
        .textContentType(.oneTimeCode)
      Button(sending ? "Verifying…" : "Verify") {
        Task {
          sending = true
          await auth.verify(code: code)
          sending = false
        }
      }
      .disabled(code.count != 6 || sending)
      Button("Use a different number") {
        code = ""
        auth.cancelCodeEntry()
      }
      .foregroundStyle(.secondary)
    }
  }

  private var e164: String {
    country.dialCode + phoneDigits
  }
}
