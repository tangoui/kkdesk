import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:cryptography/cryptography.dart' hide Hmac;

typedef PairMessage = Map<String, dynamic>;

String encodePairBytes(List<int> bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

Uint8List decodePairBytes(String text, int size) {
  final bytes = base64Url.decode(base64Url.normalize(text));
  if (bytes.length != size || encodePairBytes(bytes) != text) {
    throw const FormatException('配对数据格式错误');
  }
  return bytes;
}

Uint8List randomPairBytes(int size) {
  final random = Random.secure();
  return Uint8List.fromList(List.generate(size, (_) => random.nextInt(256)));
}

String desktopID(List<int> publicKey) => encodePairBytes(
  sha256
      .convert([...utf8.encode('kkdesk desktop id v1\x00'), ...publicKey])
      .bytes
      .take(16)
      .toList(),
);

String fingerprint(List<int> publicKey) => sha256.convert(publicKey).toString();

String verificationCode(String code, List<int> host, List<int> phone) {
  final bytes = Uint8List.fromList(
    sha256.convert([...utf8.encode(code), ...host, ...phone]).bytes,
  );
  return (ByteData.sublistView(bytes).getUint32(0) % 10000).toString().padLeft(
    4,
    '0',
  );
}

class PairTarget {
  const PairTarget({
    required this.id,
    required this.code,
    required this.fingerprint,
    required this.lan,
    this.server,
    this.token,
    this.serverKey,
    this.hostKey,
  });

  final String id, code, fingerprint;
  final List<String> lan;
  final String? server, token, serverKey, hostKey;

  PairTarget verified(String publicKey) => PairTarget(
    id: id,
    code: '',
    fingerprint: fingerprint,
    lan: lan,
    server: server,
    token: token,
    serverKey: serverKey,
    hostKey: publicKey,
  );

  factory PairTarget.parse(String input) {
    final uri = Uri.parse(input.trim());
    final fields = uri.queryParametersAll;
    if (uri.scheme != 'kkdesk' ||
        uri.host != 'pair' ||
        uri.hasPort ||
        uri.userInfo.isNotEmpty ||
        uri.path.isNotEmpty ||
        uri.hasFragment ||
        fields.values.any((values) => values.length != 1)) {
      throw const FormatException('请扫描 kkdesk 的配对二维码');
    }
    final p = uri.queryParameters;
    final code = p['c'] ?? '', fp = p['fp'] ?? '', id = p['id'] ?? '';
    if (p['v'] != '1' ||
        !RegExp(r'^[A-Za-z0-9_-]{20,64}$').hasMatch(code) ||
        !RegExp(r'^[0-9a-f]{64}$').hasMatch(fp)) {
      throw const FormatException('无效的配对二维码');
    }
    decodePairBytes(id, 16);
    final lan = p['lan']?.split(',') ?? [];
    for (final address in lan) {
      lanEndpoint(address);
    }
    final target = PairTarget(
      id: id,
      code: code,
      fingerprint: fp,
      lan: List.unmodifiable(lan),
      server: p['s'],
      token: p['t'],
      serverKey: p['pk'],
    );
    if (target.server == null) {
      if (target.token != null || target.serverKey != null || lan.isEmpty) {
        throw const FormatException('二维码没有可用的电脑地址');
      }
    } else {
      target.serverEndpoint;
    }
    return target;
  }

  Uri get serverEndpoint {
    var address = server!.trim();
    final pin = serverKey?.trim();
    if (pin != null && !address.contains('://')) address = 'https://$address';
    final uri = Uri.parse(address);
    final ip = InternetAddress.tryParse(uri.host);
    final tls = uri.scheme == 'https' || uri.scheme == 'wss';
    final loopback = uri.host == 'localhost' || ip?.isLoopback == true;
    if (uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        uri.hasPort && (uri.port < 1 || uri.port > 65535) ||
        !(tls || loopback && (uri.scheme == 'http' || uri.scheme == 'ws')) ||
        !RegExp(r'^[^\s]{32,256}$').hasMatch(token ?? '')) {
      throw const FormatException('无效的跨网服务器配置');
    }
    if (pin != null) {
      final bytes = base64.decode(pin);
      if (!tls ||
          ip == null ||
          bytes.length != 32 ||
          base64.encode(bytes) != pin) {
        throw const FormatException('服务器公钥需要配合 HTTPS IP 地址使用');
      }
    }
    return uri.replace(
      scheme: tls ? 'https' : 'http',
      path: uri.path.isEmpty || uri.path == '/' ? '/v1/signal' : uri.path,
    );
  }

  factory PairTarget.fromJson(PairMessage json) => PairTarget(
    id: json['id'] as String,
    code: '',
    fingerprint: json['fingerprint'] as String,
    lan: (json['lan'] as List).cast<String>(),
    server: json['server'] as String?,
    token: json['token'] as String?,
    serverKey: json['serverKey'] as String?,
    hostKey: json['hostKey'] as String?,
  );

  PairMessage toJson() => {
    'id': id,
    'fingerprint': fingerprint,
    'lan': lan,
    'server': server,
    'token': token,
    'serverKey': serverKey,
    'hostKey': hostKey,
  };
}

Uri lanEndpoint(String address) {
  final uri = Uri.parse('tcp://$address');
  if (InternetAddress.tryParse(uri.host) == null ||
      !uri.hasPort ||
      uri.port < 1 ||
      uri.port > 65535 ||
      uri.userInfo.isNotEmpty ||
      uri.hasQuery ||
      uri.hasFragment ||
      uri.path.isNotEmpty) {
    throw const FormatException('无效的局域网地址');
  }
  return uri;
}

class PairTranscript {
  const PairTranscript({
    required this.purpose,
    required this.clientNonce,
    required this.hostNonce,
    required this.clientKey,
    required this.hostKey,
  });
  final String purpose, clientNonce, hostNonce, clientKey, hostKey;

  Uint8List bytes(PairMessage msg) {
    final out = BytesBuilder()..add(utf8.encode('kkdesk/mobile/v1\x00'));
    for (final field in [
      purpose,
      clientNonce,
      hostNonce,
      clientKey,
      hostKey,
      msg['type'],
      msg['purpose'],
      msg['publicKey'],
      msg['nonce'],
      msg['code'],
      msg['name'],
      msg['model'],
      msg['os'],
      msg['bypass'] ?? false,
      msg['status'],
      msg['deadline'] ?? 0,
      msg['mode'],
      msg['sdp'],
    ]) {
      final utf = utf8.encode(field?.toString() ?? '');
      final length = ByteData(4)..setUint32(0, utf.length);
      out.add(length.buffer.asUint8List());
      out.add(utf);
    }
    return out.takeBytes();
  }

  String codeProof(String code) => encodePairBytes(
    Hmac(sha256, utf8.encode(code)).convert(bytes({'type': 'pair-code'})).bytes,
  );

  Future<PairMessage> sign(KeyPair key, PairMessage msg) async {
    final signature = await Ed25519().sign(bytes(msg), keyPair: key);
    return {...msg, 'signature': encodePairBytes(signature.bytes)};
  }

  Future<bool> verify(String key, PairMessage msg) => Ed25519().verify(
    bytes(msg),
    signature: Signature(
      decodePairBytes(msg['signature'] as String, 64),
      publicKey: SimplePublicKey(
        decodePairBytes(key, 32),
        type: KeyPairType.ed25519,
      ),
    ),
  );
}
