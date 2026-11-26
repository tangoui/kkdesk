import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:cryptography/cryptography.dart';

import 'pinned_tls.dart';
import '../lan_auth.dart';
import 'protocol.dart';

class PairFailure implements Exception {
  const PairFailure(this.message);
  final String message;
  @override
  String toString() => message;
}

class PairTrustFailure extends PairFailure {
  const PairTrustFailure(super.message);
}

class PairUnpaired extends PairFailure {
  const PairUnpaired() : super('已解除配对，请重新扫码配对');
}

class PairTransport {
  PairTransport._(
    this._receive,
    this._send,
    this._close, {
    required this.relay,
    this.ice = const [],
    this.relayOnly = false,
  });

  final StreamIterator<String> _receive;
  final Future<void> Function(String) _send;
  final void Function() _close;
  final bool relay, relayOnly;
  final List<PairMessage> ice;
  bool _closed = false;

  Future<PairMessage> receive([
    Duration timeout = const Duration(seconds: 8),
  ]) async {
    final envelope = await _next(_receive, timeout);
    if (envelope['type'] == 'error') {
      throw PairFailure(envelope['error'] as String? ?? '跨网服务拒绝了请求');
    }
    final message = envelope['mobile'];
    if (message is! PairMessage) throw const PairFailure('配对响应无效');
    return message;
  }

  Future<void> send(PairMessage message) =>
      _send(jsonEncode({if (relay) 'type': 'mobile_data', 'mobile': message}));

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _close();
    await _receive.cancel();
  }

  static Future<PairTransport> lan(String address) async {
    final uri = lanEndpoint(address);
    const timeout = Duration(seconds: 2);
    final socket = await Socket.connect(uri.host, uri.port, timeout: timeout);
    final messages = StreamIterator(
      socket
          .cast<List<int>>()
          .transform(utf8.decoder)
          .transform(const LineSplitter()),
    );
    try {
      final greeting = await _next(messages, timeout);
      requireLanV2(greeting);
    } catch (error) {
      socket.destroy();
      await messages.cancel();
      if (error is LanAuthFailure) throw PairFailure(error.message);
      rethrow;
    }
    return PairTransport._(
      messages,
      (frame) async {
        socket.writeln(frame);
        await socket.flush();
      },
      socket.destroy,
      relay: false,
    );
  }

  static Future<PairTransport> server(PairTarget target, String purpose) async {
    final socket = await _webSocket(target);
    final messages = StreamIterator(socket.cast<String>());
    try {
      socket.add(
        jsonEncode({
          'type': purpose == 'connect' ? 'mobile_connect' : 'mobile',
          'device': target.id,
        }),
      );
      final ready = await _next(messages, const Duration(seconds: 6));
      if (ready['type'] != 'mobile_ready') {
        throw PairFailure(ready['error'] as String? ?? '跨网信令无效');
      }
      return PairTransport._(
        messages,
        (frame) async {
          socket.add(frame);
        },
        () {
          unawaited(socket.close());
        },
        relay: true,
        ice: (ready['ice'] as List? ?? const []).cast<PairMessage>(),
        relayOnly: ready['relayOnly'] == true,
      );
    } catch (_) {
      unawaited(socket.close());
      await messages.cancel();
      rethrow;
    }
  }

  static Future<PairMessage> _next(
    StreamIterator<String> messages,
    Duration timeout,
  ) async {
    if (!await messages.moveNext().timeout(timeout)) {
      throw const SocketException('电脑已关闭连接');
    }
    final wire = messages.current;
    if (wire.length > 128 * 1024) throw const PairFailure('配对消息过长');
    return jsonDecode(wire) as PairMessage;
  }
}

class AuthenticatedPair {
  AuthenticatedPair(this.transport, this.transcript, this.challenge, this.key);
  final PairTransport transport;
  final PairTranscript transcript;
  final PairMessage challenge;
  final SimpleKeyPair key;
  bool get bypass => challenge['bypass'] == true;
  Future<PairMessage> Function(PairMessage offer)? protectOffer;

  static Future<AuthenticatedPair> start(
    PairTarget target,
    PairTransport transport,
    String purpose,
    SimpleKeyPair key,
  ) async {
    final local = encodePairBytes((await key.extractPublicKey()).bytes);
    final nonce = encodePairBytes(randomPairBytes(32));
    await transport.send({
      'type': 'hello',
      'purpose': purpose,
      'publicKey': local,
      'nonce': nonce,
    });
    final challenge = await transport.receive(
      transport.relay ? const Duration(seconds: 6) : const Duration(seconds: 2),
    );
    try {
      final remote = challenge['publicKey'] as String;
      final public = decodePairBytes(remote, 32);
      final hostNonce = challenge['nonce'] as String;
      decodePairBytes(hostNonce, 32);
      final transcript = PairTranscript(
        purpose: purpose,
        clientNonce: nonce,
        hostNonce: hostNonce,
        clientKey: local,
        hostKey: remote,
      );
      final matchingIdentity = target.manual
          ? transcript.verifyManualHost(target.code, challenge['code'])
          : (target.hostKey == null || target.hostKey == remote) &&
                desktopID(public) == target.id &&
                fingerprint(public) == target.fingerprint;
      if (challenge['type'] != 'challenge' ||
          !matchingIdentity ||
          !await transcript.verify(remote, challenge)) {
        throw PairTrustFailure(target.manual ? '手动配对码错误或已过期' : '电脑身份与配对信息不匹配');
      }
      return AuthenticatedPair(transport, transcript, challenge, key);
    } on FormatException {
      throw const PairTrustFailure('电脑没有提供有效的身份签名');
    } on TypeError {
      throw const PairTrustFailure('电脑没有提供有效的身份签名');
    }
  }

  Future<void> send(PairMessage msg) async {
    final protect = protectOffer;
    if (msg['type'] == 'offer' && protect != null) {
      msg = await protect(msg);
    }
    await transport.send(await transcript.sign(key, msg));
  }

  Future<PairMessage> receive([
    Duration timeout = const Duration(seconds: 8),
  ]) async {
    final msg = await transport.receive(timeout);
    if (!await transcript.verify(transcript.hostKey, msg)) {
      throw const PairTrustFailure('电脑响应的签名无效');
    }
    if (msg['status'] == 'unpaired') throw const PairUnpaired();
    return msg;
  }
}

Future<WebSocket> _webSocket(PairTarget target) async {
  final endpoint = target.serverEndpoint;
  final pin = target.serverKey?.trim();
  final client = HttpClient()
    ..connectionTimeout = const Duration(seconds: 5)
    ..findProxy = (_) => 'DIRECT';
  var httpEndpoint = endpoint;
  if (pin != null) {
    final expected = base64.decode(pin);
    // The factory supplies a TLS socket after verifying the pinned key.
    // HTTP uses that stream without starting a second TLS handshake.
    httpEndpoint = endpoint.replace(scheme: 'http', port: endpoint.port);
    client.connectionFactory = (_, _, _) =>
        openPinnedConnection(endpoint.host, endpoint.port, expected);
  }
  try {
    return await _upgrade(
      client,
      httpEndpoint,
      target.token!,
    ).timeout(const Duration(seconds: 8));
  } finally {
    client.close(force: true);
  }
}

Future<WebSocket> _upgrade(
  HttpClient client,
  Uri endpoint,
  String token,
) async {
  final nonce = base64.encode(randomPairBytes(16));
  final request = await client.getUrl(endpoint);
  request.followRedirects = false;
  request.headers
    ..set(HttpHeaders.authorizationHeader, 'Bearer $token')
    ..set(HttpHeaders.connectionHeader, 'Upgrade')
    ..set(HttpHeaders.upgradeHeader, 'websocket')
    ..set('Sec-WebSocket-Key', nonce)
    ..set('Sec-WebSocket-Version', '13');
  final reply = await request.close();
  final expected = base64.encode(
    sha1
        .convert(
          ascii.encode(
            '$nonce'
            '258EAFA5-E914-47DA-95CA-C5AB0DC85B11',
          ),
        )
        .bytes,
  );
  if (reply.statusCode != HttpStatus.switchingProtocols ||
      reply.headers.value(HttpHeaders.upgradeHeader)?.toLowerCase() !=
          'websocket' ||
      !(reply.headers.value(HttpHeaders.connectionHeader) ?? '')
          .toLowerCase()
          .split(',')
          .any((value) => value.trim() == 'upgrade') ||
      reply.headers.value('Sec-WebSocket-Accept') != expected) {
    throw PairFailure(reply.statusCode == 401 ? '跨网服务密钥不正确' : '无法连接跨网服务');
  }
  return WebSocket.fromUpgradedSocket(
    await reply.detachSocket(),
    serverSide: false,
  );
}
