import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:cryptography/cryptography.dart' hide Hmac;
import 'package:shared_preferences/shared_preferences.dart';

import 'pairing/protocol.dart';

class LanAuthFailure implements Exception {
  const LanAuthFailure(this.message);

  final String message;

  @override
  String toString() => message;
}

const invalidLanIdentity = LanAuthFailure(
  '对方的身份签名无效，已断开（对方可能是旧版本 kkdesk，请两边都升级到最新版）',
);

void requireLanV2(Map<String, dynamic> greeting) {
  if (greeting['version'] is! int || greeting['version'] != 2) {
    throw invalidLanIdentity;
  }
}

Future<String> verifyLanAnswer(
  String offer,
  String answer,
  Object? key,
  Object? signature,
) async {
  if (key is! String || signature is! String) throw invalidLanIdentity;
  try {
    final message = BytesBuilder()
      ..add(utf8.encode('kkdesk host answer v2\x00'));
    for (final sdp in [offer, answer]) {
      final bytes = utf8.encode(sdp);
      final length = ByteData(4)..setUint32(0, bytes.length);
      message
        ..add(length.buffer.asUint8List())
        ..add(bytes);
    }
    if (await Ed25519().verify(
      message.takeBytes(),
      signature: Signature(
        decodePairBytes(signature, 64),
        publicKey: SimplePublicKey(
          decodePairBytes(key, 32),
          type: KeyPairType.ed25519,
        ),
      ),
    )) {
      return key;
    }
  } on FormatException {
    throw invalidLanIdentity;
  }
  throw invalidLanIdentity;
}

Uint8List lanPasswordProof(String password, String offer, String answer) {
  final proof = Hmac(sha256, utf8.encode(password)).convert([
    ...utf8.encode('kkdesk password v2\x00'),
    ...sha256.convert(utf8.encode(offer)).bytes,
    ...sha256.convert(utf8.encode(answer)).bytes,
  ]);
  return Uint8List.fromList([0x41, ...proof.bytes]);
}

class LanAuthResult {
  const LanAuthResult._(this.failure);

  final String? failure;

  static LanAuthResult? decode(List<int> bytes) {
    if (bytes.isEmpty || bytes[0] != 0x52) return null;
    if (bytes.length == 2) {
      return switch (bytes[1]) {
        0 => const LanAuthResult._(null),
        1 => const LanAuthResult._('密码错误'),
        _ => null,
      };
    }
    if (bytes.length == 6 && bytes[1] == 2) {
      final seconds = ByteData.sublistView(Uint8List.fromList(bytes))
          .getUint32(2);
      if (seconds > 0) {
        return LanAuthResult._('对方因多次密码错误暂停了连接，请 $seconds 秒后再试');
      }
    }
    return null;
  }
}

String hostDeviceCode(String key) {
  final id = desktopID(decodePairBytes(key, 32));
  final digest = sha256.convert(utf8.encode(id)).bytes;
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  final code = StringBuffer('KD-');
  // The first twelve base32 characters encode the first sixty digest bits.
  for (var bit = 0; bit < 60; bit += 5) {
    final index = bit ~/ 8;
    final pair = (digest[index] << 8) | digest[index + 1];
    code.write(alphabet[(pair >> (11 - bit % 8)) & 31]);
  }
  return code.toString();
}

Uri passwordLanEndpoint(String address) {
  final endpoint = Uri.tryParse('tcp://${address.trim()}');
  if (endpoint == null ||
      endpoint.host.isEmpty ||
      endpoint.userInfo.isNotEmpty ||
      endpoint.path.isNotEmpty ||
      endpoint.hasQuery ||
      endpoint.hasFragment ||
      (endpoint.hasPort && (endpoint.port < 1 || endpoint.port > 65535))) {
    throw const FormatException('请输入电脑地址，可附加端口');
  }
  return endpoint.replace(port: endpoint.hasPort ? endpoint.port : 21118);
}

class LanIdentityChanged extends LanAuthFailure {
  LanIdentityChanged(this._store, this._endpoint)
    : super('这台电脑的身份变了（可能重装了 kkdesk，也可能有人冒充）。连接已断开。');

  final LanIdentities _store;
  final Uri _endpoint;

  Future<void> forget() => _store.forget(_endpoint);
}

class LanIdentities {
  LanIdentities([SharedPreferencesAsync? preferences])
    : _preferences = preferences ?? SharedPreferencesAsync();

  final SharedPreferencesAsync _preferences;
  static Future<void> _writes = Future.value();

  String _record(Uri endpoint) => 'lan.identity.v2.${endpoint.authority}';

  Future<String?> _check(Uri endpoint, String key) async {
    final previous = await _preferences.getString(_record(endpoint));
    if (previous != null && previous != key) {
      throw LanIdentityChanged(this, endpoint);
    }
    return previous;
  }

  Future<void> check(Uri endpoint, String key) async {
    await _writes;
    await _check(endpoint, key);
  }

  Future<String?> remember(Uri endpoint, String key) => _write(() async {
    if (await _check(endpoint, key) != null) return null;
    await _preferences.setString(_record(endpoint), key);
    return hostDeviceCode(key);
  });

  Future<void> forget(Uri endpoint) =>
      _write(() => _preferences.remove(_record(endpoint)));

  Future<T> _write<T>(Future<T> Function() operation) {
    final result = _writes.then((_) => operation());
    _writes = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }
}

/// The longest signaling line accepted from a computer, as on the desktop.
const maxSignalLine = 256 * 1024;

/// Splits [bytes] into UTF-8 lines like `utf8.decoder` with a
/// [LineSplitter], but fails with a [FormatException] once a line grows past
/// [maxSignalLine] bytes, so a peer cannot make the phone buffer without
/// bound.
Stream<String> boundedLines(Stream<List<int>> bytes) async* {
  var pending = <int>[];
  await for (final chunk in bytes) {
    var start = 0;
    for (var i = 0; i < chunk.length; i++) {
      if (chunk[i] != 0x0A) continue;
      pending.addAll(chunk.sublist(start, i));
      if (pending.length > maxSignalLine) {
        throw const FormatException('signaling message too long');
      }
      var line = utf8.decode(pending);
      if (line.endsWith('\r')) line = line.substring(0, line.length - 1);
      yield line;
      pending = <int>[];
      start = i + 1;
    }
    pending.addAll(chunk.sublist(start));
    if (pending.length > maxSignalLine) {
      throw const FormatException('signaling message too long');
    }
  }
  if (pending.isNotEmpty) yield utf8.decode(pending);
}
