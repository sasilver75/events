import Foundation
import Supabase

// Avatar uploads to the public-read `avatars` bucket (#88). Mirror of
// BannerStorage in EventsAPI.swift — same per-user-prefix path layout
// (`{userID}/{uuid}.jpg`) and same RLS posture (storage.foldername(name)[1]
// must equal auth.uid()).
enum AvatarStorage {
  static let bucket = "avatars"

  static func upload(jpegData: Data, userID: UUID) async throws -> String {
    let path = "\(userID.uuidString.lowercased())/\(UUID().uuidString.lowercased()).jpg"
    _ = try await SupabaseConfig.shared.storage
      .from(bucket)
      .upload(
        path,
        data: jpegData,
        options: FileOptions(contentType: "image/jpeg", upsert: false)
      )
    return path
  }

  static func publicURL(forPath path: String) -> URL? {
    try? SupabaseConfig.shared.storage.from(bucket).getPublicURL(path: path)
  }
}
