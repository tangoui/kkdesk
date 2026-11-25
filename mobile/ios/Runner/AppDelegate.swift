import Flutter
import UIKit
import WebRTC
import flutter_webrtc

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  private var frameProbe: FrameProbe?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "KKDeskSpike") else {
      return
    }
    let channel = FlutterMethodChannel(name: "kkdesk/spike", binaryMessenger: registrar.messenger())
    channel.setMethodCallHandler { [weak self] call, result in
      switch call.method {
      case "startFrameProbe":
        guard let trackID = call.arguments as? String,
              let track = FlutterWebRTCPlugin.sharedSingleton()?.remoteTrack(forId: trackID)
                as? RTCVideoTrack else {
          result(FlutterError(code: "no-track", message: "Remote track unavailable", details: nil))
          return
        }
        self?.frameProbe?.detach()
        self?.frameProbe = FrameProbe(track: track)
        UIApplication.shared.isIdleTimerDisabled = true
        result(nil)
      case "readFrameProbe":
        result([
          "frames": self?.frameProbe?.drain() ?? [],
          "thermalState": ProcessInfo.processInfo.thermalState.rawValue,
        ])
      case "stopFrameProbe":
        self?.frameProbe?.detach()
        self?.frameProbe = nil
        UIApplication.shared.isIdleTimerDisabled = false
        result(nil)
      case "appendStats":
        do {
          if let line = call.arguments as? String {
            try self?.appendStats(line)
          }
          result(nil)
        } catch {
          result(FlutterError(code: "stats-write", message: error.localizedDescription, details: nil))
        }
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  private func appendStats(_ line: String) throws {
    let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
    let file = documents.appendingPathComponent("kkdesk-spike.jsonl")
    let data = Data((line + "\n").utf8)
    if !FileManager.default.fileExists(atPath: file.path) {
      try data.write(to: file)
      return
    }
    let handle = try FileHandle(forWritingTo: file)
    defer { try? handle.close() }
    try handle.seekToEnd()
    try handle.write(contentsOf: data)
  }
}

// The decoder delivers the original RTP timestamp. Read the wall clock on
// this thread, before hopping to Dart, to match kkdesk's capture messages.
private final class FrameProbe: NSObject, RTCVideoRenderer {
  private var track: RTCVideoTrack?
  private let lock = NSLock()
  private var frames: [[Int64]] = []

  init(track: RTCVideoTrack) {
    self.track = track
    super.init()
    track.add(self)
  }

  func detach() {
    track?.remove(self)
    track = nil
  }

  func setSize(_ size: CGSize) {}

  func renderFrame(_ frame: RTCVideoFrame?) {
    guard let frame else { return }
    let receivedAt = Int64(Date().timeIntervalSince1970 * 1e9)
    lock.lock()
    if frames.count < 240 {
      frames.append([Int64(UInt32(bitPattern: frame.timeStamp)), receivedAt])
    }
    lock.unlock()
  }

  func drain() -> [[Int64]] {
    lock.lock()
    defer { lock.unlock() }
    let result = frames
    frames.removeAll(keepingCapacity: true)
    return result
  }
}
