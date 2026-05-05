import Foundation
import Supabase

enum SupabaseConfig {
  static let shared: SupabaseClient = {
    guard
      let urlString = Bundle.main.object(forInfoDictionaryKey: "SUPABASE_URL") as? String,
      let url = URL(string: urlString),
      let anonKey = Bundle.main.object(forInfoDictionaryKey: "SUPABASE_ANON_KEY") as? String,
      !anonKey.isEmpty
    else {
      fatalError(
        "SUPABASE_URL / SUPABASE_ANON_KEY missing — check Local.xcconfig and target config wiring")
    }
    return SupabaseClient(supabaseURL: url, supabaseKey: anonKey)
  }()

  static var serverURL: URL {
    guard
      let s = Bundle.main.object(forInfoDictionaryKey: "SERVER_URL") as? String,
      let url = URL(string: s)
    else {
      fatalError("SERVER_URL missing — check Local.xcconfig")
    }
    return url
  }
}
