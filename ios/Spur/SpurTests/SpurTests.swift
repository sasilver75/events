import Foundation
import Testing

@testable import Spur

// MARK: EventCategory

@Suite("EventCategory")
struct EventCategoryTests {

  @Test("known server strings round-trip to the matching case")
  func knownStrings() {
    #expect(EventCategory.from("Sports") == .sports)
    #expect(EventCategory.from("Food/Drink") == .foodDrink)
    #expect(EventCategory.from("Outdoors") == .outdoors)
    #expect(EventCategory.from("Other") == .other)
  }

  // Guards against a future server-side category addition crashing the iOS
  // client — the lenient mapping must collapse unknowns to .other rather
  // than returning nil and forcing every call site to handle it.
  @Test("unknown server string collapses to .other")
  func unknownString() {
    #expect(EventCategory.from("Volcanology") == .other)
    #expect(EventCategory.from("") == .other)
  }
}

// MARK: ISO-8601 with fractional seconds

@Suite("EventsAPI date decoding")
struct EventsAPIDateDecodingTests {

  // Postgres TIMESTAMPTZ marshals as RFC3339 with fractional seconds
  // (e.g. 2026-05-06T16:00:00.123456Z). Foundation's stock .iso8601 chokes
  // on the fractional part. The custom strategy must accept both shapes
  // so a future Postgres / sqlc / framework change to the wire format
  // doesn't silently break Browse.
  private struct Wrap: Decodable { let d: Date }

  private func decode(_ s: String) throws -> Date {
    let json = #"{"d":"\#(s)"}"#.data(using: .utf8)!
    let dec = JSONDecoder()
    dec.dateDecodingStrategy = .iso8601withFractionalSeconds
    return try dec.decode(Wrap.self, from: json).d
  }

  @Test("accepts ISO-8601 with fractional seconds")
  func withFractional() throws {
    let d = try decode("2026-05-06T16:00:00.123456Z")
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    let expected = formatter.date(from: "2026-05-06T16:00:00.123456Z")!
    #expect(d == expected)
  }

  @Test("accepts ISO-8601 without fractional seconds")
  func withoutFractional() throws {
    let d = try decode("2026-05-06T16:00:00Z")
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime]
    let expected = formatter.date(from: "2026-05-06T16:00:00Z")!
    #expect(d == expected)
  }

  @Test("rejects garbage")
  func rejectsGarbage() {
    #expect(throws: DecodingError.self) {
      try decode("not-a-date")
    }
  }
}
