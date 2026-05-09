import Foundation
import Supabase
import UIKit

// AvatarStorage uploads selfie JPEGs to the public-read `avatars` bucket
// (#88, migration 0017). Mirrors BannerStorage in EventsAPI.swift — same
// path layout `{userID}/{uuid}.jpg`, same stepped JPEG quality compression
// to fit the 2 MiB cap, same getPublicURL flow for rendering.
//
// The path is what `POST /users/me/avatar` validates (must start with
// `{auth.uid()}/`) and what gets persisted in users.avatar_path.
enum AvatarStorage {
  static let bucket = "avatars"
  static let maxBytes = 2 * 1024 * 1024  // 2 MiB
  static let maxLongSidePx: CGFloat = 1024  // selfies don't need to be larger than this

  enum AvatarError: LocalizedError {
    case encode
    case tooLargeAfterCompression

    var errorDescription: String? {
      switch self {
      case .encode: return "Couldn't encode the image."
      case .tooLargeAfterCompression:
        return "Image is too large even after compression — pick a smaller photo."
      }
    }
  }

  // upload re-encodes the input UIImage to a JPEG under the 2 MiB cap and
  // PUTs it to the avatars bucket. Returns the storage path so the caller
  // can pass it on to `POST /users/me/avatar`.
  static func upload(image: UIImage, userID: UUID) async throws -> String {
    let jpegData = try compress(image: image)
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

  // compress downscales the image to maxLongSidePx, then steps JPEG quality
  // down from 0.85 → 0.30 in 0.10 increments until under the cap. Mirrors
  // the banner-upload pattern in CreateEventSheet.compressUnderCap.
  private static func compress(image: UIImage) throws -> Data {
    let scaled = downscale(image, maxLongSidePx: maxLongSidePx)
    var quality: CGFloat = 0.85
    while quality >= 0.30 {
      guard let data = scaled.jpegData(compressionQuality: quality) else {
        throw AvatarError.encode
      }
      if data.count <= maxBytes { return data }
      quality -= 0.10
    }
    throw AvatarError.tooLargeAfterCompression
  }

  private static func downscale(_ image: UIImage, maxLongSidePx: CGFloat) -> UIImage {
    let longest = max(image.size.width, image.size.height)
    guard longest > maxLongSidePx else { return image }
    let scale = maxLongSidePx / longest
    let target = CGSize(width: image.size.width * scale, height: image.size.height * scale)
    let renderer = UIGraphicsImageRenderer(size: target)
    return renderer.image { _ in
      image.draw(in: CGRect(origin: .zero, size: target))
    }
  }
}
