import Flutter
import ImageIO
import PhotosUI
import UniformTypeIdentifiers

final class MediaUploadPicker: NSObject, PHPickerViewControllerDelegate,
    UIAdaptivePresentationControllerDelegate {
  private var pending: FlutterResult?

  func register(with messenger: FlutterBinaryMessenger) {
    let channel = FlutterMethodChannel(name: "kkdesk/media_upload", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in
      guard call.method == "pick" else {
        result(FlutterMethodNotImplemented)
        return
      }
      self?.pick(result)
    }
  }

  private func pick(_ result: @escaping FlutterResult) {
    guard pending == nil else {
      result(FlutterError(code: "media_picker_busy", message: nil, details: nil))
      return
    }
    let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
    guard var presenter = scenes.first(where: { $0.activationState == .foregroundActive })?
      .keyWindow?.rootViewController else {
      result(FlutterError(code: "media_picker", message: nil, details: nil))
      return
    }
    while let presented = presenter.presentedViewController { presenter = presented }

    var configuration = PHPickerConfiguration(photoLibrary: .shared())
    configuration.filter = .any(of: [.images, .videos])
    configuration.selectionLimit = 0
    configuration.selection = .ordered
    configuration.preferredAssetRepresentationMode = .current
    let picker = PHPickerViewController(configuration: configuration)
    picker.delegate = self
    picker.presentationController?.delegate = self
    pending = result
    presenter.present(picker, animated: true)
  }

  func presentationControllerDidDismiss(_ presentationController: UIPresentationController) {
    complete([])
  }

  func picker(_ picker: PHPickerViewController, didFinishPicking items: [PHPickerResult]) {
    picker.dismiss(animated: true)
    guard !items.isEmpty else {
      complete([])
      return
    }
    let importedAt = Date()
    Task { @MainActor in
      var exported: [[String: Any]] = []
      do {
        for item in items {
          exported.append(try await Self.export(item, importedAt: importedAt))
        }
        complete(exported)
      } catch {
        for item in exported {
          if let path = item["path"] as? String {
            try? FileManager.default.removeItem(atPath: path)
          }
        }
        complete(FlutterError(code: "media_export", message: nil, details: nil))
      }
    }
  }

  private func complete(_ value: Any) {
    let callback = pending
    pending = nil
    callback?(value)
  }

  private static func export(_ item: PHPickerResult, importedAt: Date) async throws -> [String: Any] {
    let provider = item.itemProvider
    let type = provider.registeredTypeIdentifiers.lazy.compactMap(UTType.init)
      .first(where: {
        $0.preferredFilenameExtension != nil &&
          ($0.conforms(to: .image) || $0.conforms(to: .movie))
      })
    let video = type?.conforms(to: .movie) ??
      provider.hasItemConformingToTypeIdentifier(UTType.movie.identifier)
    let typeIdentifier = (type ?? (video ? .movie : .image)).identifier
    let photoAccess = PHPhotoLibrary.authorizationStatus(for: .readWrite)
    let asset = item.assetIdentifier.flatMap { identifier in
      guard photoAccess == .authorized || photoAccess == .limited else { return nil as PHAsset? }
      return PHAsset.fetchAssets(withLocalIdentifiers: [identifier], options: nil).firstObject
    }
    let resource = asset.flatMap { asset in
      PHAssetResource.assetResources(for: asset)
        .first(where: { $0.type == (video ? .video : .photo) })
    }

    return try await withCheckedThrowingContinuation { continuation in
      provider.loadFileRepresentation(forTypeIdentifier: typeIdentifier) { url, error in
        do {
          guard let url else { throw error ?? CocoaError(.fileReadUnknown) }
          let folder = FileManager.default.temporaryDirectory
            .appendingPathComponent("media-uploads", isDirectory: true)
          try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
          let urlType = UTType(filenameExtension: url.pathExtension)
          let extensionName = urlType?.conforms(to: video ? .movie : .image) == true
            ? url.pathExtension : type?.preferredFilenameExtension ?? ""
          let destination = folder.appendingPathComponent(UUID().uuidString)
          try FileManager.default.copyItem(at: url, to: destination)
          let timestamp = asset?.creationDate ?? (video ? nil : photoDate(url)) ?? importedAt
          var selected: [String: Any] = [
            "path": destination.path,
            "isOriginalName": resource != nil,
            "exportedName": url.lastPathComponent,
            "extension": extensionName,
            "video": video,
            "timestampMs": Int64(timestamp.timeIntervalSince1970 * 1000),
          ]
          selected["name"] = resource?.originalFilename ?? provider.suggestedName
          continuation.resume(returning: selected)
        } catch {
          continuation.resume(throwing: error)
        }
      }
    }
  }

  private static func photoDate(_ url: URL) -> Date? {
    guard let source = CGImageSourceCreateWithURL(url as CFURL, nil),
          let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
          let exif = properties[kCGImagePropertyExifDictionary] as? [CFString: Any],
          let captured = exif[kCGImagePropertyExifDateTimeOriginal] as? String else { return nil }
    let formatter = DateFormatter()
    formatter.locale = Locale(identifier: "en_US_POSIX")
    formatter.dateFormat = "yyyy:MM:dd HH:mm:ss"
    return formatter.date(from: captured)
  }
}
