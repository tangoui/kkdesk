import 'package:flutter/services.dart';

/// A device-scoped P-256 signing key backed by the platform's biometric hardware.
class BiometricKey {
  const BiometricKey();

  static const _channel = MethodChannel('kkdesk/biometric-key');

  Future<Uint8List> prepare(String id) async {
    final publicKey = await _invoke<Uint8List>('prepare', {'id': id});
    if (publicKey == null || publicKey.length != 65 || publicKey[0] != 4) {
      throw PlatformException(code: 'unavailable');
    }
    return publicKey;
  }

  Future<Uint8List> sign(String id, Uint8List message) async {
    final signature = await _invoke<Uint8List>('sign', {
      'id': id,
      'message': message,
    });
    if (signature == null || signature.isEmpty) {
      throw PlatformException(code: 'unavailable');
    }
    return signature;
  }

  Future<void> remove(String id) => _invoke<void>('remove', {'id': id});

  Future<T?> _invoke<T>(String method, Map<String, Object> arguments) async {
    try {
      return await _channel.invokeMethod<T>(method, arguments);
    } on MissingPluginException {
      throw PlatformException(code: 'unavailable');
    }
  }
}
