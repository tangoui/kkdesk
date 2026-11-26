import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';

String challengeMac(String password, String nonce) {
  final bytes = base64Decode(nonce);
  if (bytes.length != 32) throw const FormatException('无效的密码验证请求');
  return base64Encode(Hmac(sha256, utf8.encode(password)).convert(bytes).bytes);
}

Uint8List mouseMove(double x, double y) =>
    _position(1, 9, x, y).buffer.asUint8List();

Uint8List mouseButton(double x, double y, bool down) {
  final data = _position(2, 12, x, y)..setUint8(10, down ? 1 : 0);
  return data.buffer.asUint8List();
}

ByteData _position(int kind, int size, double x, double y) => ByteData(size)
  ..setUint8(0, kind)
  ..setFloat32(1, x, Endian.little)
  ..setFloat32(5, y, Endian.little);

/// Host accessibility status on the reliable meta channel.
bool? decodeAccessibility(List<int> bytes) {
  if (bytes.length != 2 || bytes[0] != 0x61 || bytes[1] > 1) return null;
  return bytes[1] == 1;
}

class ClockSync {
  final _samples = <({int offset, int rtt})>[];

  Uint8List ping() =>
      (ByteData(9)
            ..setUint8(0, 0x70)
            ..setInt64(1, nowNanos()))
          .buffer
          .asUint8List();

  void receive(Uint8List bytes, int receivedAt) {
    if (bytes.length != 17 || bytes[0] != 0x6f) return;
    final data = ByteData.sublistView(bytes);
    final sentAt = data.getInt64(1);
    final rtt = receivedAt - sentAt;
    if (rtt < 0) return;
    if (_samples.length == 10) _samples.removeAt(0);
    _samples.add((offset: data.getInt64(9) - sentAt - rtt ~/ 2, rtt: rtt));
  }

  ({int offset, int rtt})? get best => _samples.isEmpty
      ? null
      : _samples.reduce((a, b) => a.rtt < b.rtt ? a : b);

  static int nowNanos() => DateTime.now().microsecondsSinceEpoch * 1000;
}
