import CryptoKit
import Flutter
import LocalAuthentication
import Security

final class BiometricKey {
  private let queue = DispatchQueue(label: "kkdesk.biometric-key", qos: .userInitiated)
  private var busy = false

  func register(with messenger: FlutterBinaryMessenger) {
    let channel = FlutterMethodChannel(name: "kkdesk/biometric-key", binaryMessenger: messenger)
    channel.setMethodCallHandler { [self] call, result in
      guard ["prepare", "sign", "remove"].contains(call.method) else {
        result(FlutterMethodNotImplemented)
        return
      }
      guard let arguments = call.arguments as? [String: Any],
            let id = arguments["id"] as? String, Self.isValidID(id) else {
        result(Failure("invalid-argument").flutterError)
        return
      }
      let message = (arguments["message"] as? FlutterStandardTypedData)?.data
      if call.method == "sign" && message == nil {
        result(Failure("invalid-argument").flutterError)
        return
      }
      guard !busy else {
        result(Failure("busy").flutterError)
        return
      }
      busy = true
      let method = call.method
      queue.async {
        let response: Any?
        do {
          let tag = Data("kkdesk.biometric-key.v1.\(id)".utf8)
          let query = self.keyQuery(tag)
          switch method {
          case "prepare":
            response = FlutterStandardTypedData(bytes: try self.prepare(tag: tag, query: query))
          case "sign":
            guard let message else { throw Failure("invalid-argument") }
            response = FlutterStandardTypedData(bytes: try self.sign(query, message: message))
          default:
            try self.remove(query)
            response = nil
          }
        } catch let failure as Failure {
          response = failure.flutterError
        } catch {
          response = Failure("unavailable").flutterError
        }
        DispatchQueue.main.async {
          self.busy = false
          result(response)
        }
      }
    }
  }

  private static func isValidID(_ id: String) -> Bool {
    let bytes = id.utf8
    return (1...128).contains(bytes.count) && bytes.allSatisfy {
      (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0)
        || $0 == 45 || $0 == 95
    }
  }

  private func keyQuery(_ tag: Data) -> [String: Any] {
    [
      kSecClass as String: kSecClassKey,
      kSecAttrApplicationTag as String: tag,
      kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
      kSecAttrKeyClass as String: kSecAttrKeyClassPrivate,
      kSecAttrTokenID as String: kSecAttrTokenIDSecureEnclave,
    ]
  }

  private func privateKey(_ query: [String: Any], context: LAContext) throws -> SecKey? {
    var attributes = query
    attributes[kSecUseAuthenticationContext as String] = context
    attributes[kSecReturnRef as String] = true
    var item: CFTypeRef?
    let status = SecItemCopyMatching(attributes as CFDictionary, &item)
    switch status {
    case errSecSuccess:
      guard let item, CFGetTypeID(item) == SecKeyGetTypeID() else {
        throw Failure("unavailable")
      }
      return (item as! SecKey)
    case errSecItemNotFound:
      return nil
    default:
      throw Failure(status)
    }
  }

  private func prepare(tag: Data, query: [String: Any]) throws -> Data {
    guard SecureEnclave.isAvailable else { throw Failure("unavailable") }
    let context = LAContext()
    defer { context.invalidate() }
    context.interactionNotAllowed = true
    guard context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil) else {
      throw Failure("unavailable")
    }
    if let key = try privateKey(query, context: context) {
      return try publicKeyBytes(key)
    }
    var error: Unmanaged<CFError>?
    guard let access = SecAccessControlCreateWithFlags(
      nil, kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly,
      [.biometryCurrentSet, .and, .privateKeyUsage], &error
    ) else {
      throw Failure(error)
    }
    let attributes: [String: Any] = [
      kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
      kSecAttrKeySizeInBits as String: 256,
      kSecAttrTokenID as String: kSecAttrTokenIDSecureEnclave,
      kSecPrivateKeyAttrs as String: [
        kSecAttrIsPermanent as String: true,
        kSecAttrApplicationTag as String: tag,
        kSecAttrAccessControl as String: access,
      ],
    ]
    guard let key = SecKeyCreateRandomKey(attributes as CFDictionary, &error) else {
      throw Failure(error)
    }
    return try publicKeyBytes(key)
  }

  private func publicKeyBytes(_ key: SecKey) throws -> Data {
    guard let publicKey = SecKeyCopyPublicKey(key) else { throw Failure("unavailable") }
    var error: Unmanaged<CFError>?
    guard let bytes = SecKeyCopyExternalRepresentation(publicKey, &error) as Data? else {
      throw Failure(error)
    }
    guard bytes.count == 65, bytes.first == 4 else { throw Failure("unavailable") }
    return bytes
  }

  private func sign(_ query: [String: Any], message: Data) throws -> Data {
    let context = LAContext()
    defer { context.invalidate() }
    context.localizedReason = "验证生物识别以连接已配对的电脑"
    context.localizedFallbackTitle = ""
    context.touchIDAuthenticationAllowableReuseDuration = 0
    guard let key = try privateKey(query, context: context) else {
      throw Failure("missing")
    }
    var policyError: NSError?
    guard context.canEvaluatePolicy(
      .deviceOwnerAuthenticationWithBiometrics, error: &policyError
    ) else {
      throw Failure(policyError, signing: true)
    }
    var error: Unmanaged<CFError>?
    // The key operation evaluates the device's current biometric set through this context.
    guard let signature = SecKeyCreateSignature(
      key, .ecdsaSignatureMessageX962SHA256, message as CFData, &error
    ) as Data? else {
      throw Failure(error, signing: true)
    }
    return signature
  }

  private func remove(_ query: [String: Any]) throws {
    let status = SecItemDelete(query as CFDictionary)
    guard status == errSecSuccess || status == errSecItemNotFound else {
      throw Failure(status)
    }
  }

  private struct Failure: Error {
    let code: String

    init(_ code: String) {
      self.code = code
    }

    init(_ status: OSStatus) {
      switch status {
      case errSecUserCanceled:
        self.init("cancelled")
      case errSecItemNotFound:
        self.init("missing")
      case errSecAuthFailed:
        self.init("invalidated")
      default:
        self.init("unavailable")
      }
    }

    init(_ error: Unmanaged<CFError>?, signing: Bool = false) {
      let error = error.map { $0.takeRetainedValue() as Error as NSError }
      self.init(error, signing: signing)
    }

    init(_ error: NSError?, signing: Bool) {
      if let underlying = error?.userInfo[NSUnderlyingErrorKey] as? NSError {
        self.init(underlying, signing: signing)
      } else if let error, error.domain == LAError.errorDomain {
        switch LAError.Code(rawValue: error.code) {
        case .userCancel, .userFallback, .systemCancel, .appCancel:
          self.init("cancelled")
        case .biometryNotEnrolled, .passcodeNotSet:
          self.init(signing ? "invalidated" : "unavailable")
        default:
          self.init("unavailable")
        }
      } else if let error, error.domain == NSOSStatusErrorDomain {
        self.init(OSStatus(truncatingIfNeeded: error.code))
      } else {
        self.init("unavailable")
      }
    }

    var flutterError: FlutterError {
      FlutterError(code: code, message: nil, details: nil)
    }
  }
}
