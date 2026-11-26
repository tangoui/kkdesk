import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:asn1lib/asn1lib.dart';
import 'package:tlslite/tlslite.dart' as tls;

import 'transport.dart';

bool matchesServerCertificate(Uint8List der, List<int> expected) {
  try {
    final certificate = ASN1Parser(der).nextObject() as ASN1Sequence;
    final tbs = certificate.elements.first as ASN1Sequence;
    // The optional explicit version precedes the serial number in TBSCertificate.
    final start = tbs.elements.first.tag == 0xa0 ? 1 : 0;
    final spki = tbs.elements[start + 5] as ASN1Sequence;
    final algorithm = spki.elements[0] as ASN1Sequence;
    final oid = algorithm.elements.single as ASN1ObjectIdentifier;
    final key = spki.elements[1] as ASN1BitString;
    return oid.identifier == '1.3.101.112' &&
        key.unusedbits == 0 &&
        expected.length == 32 &&
        _equal(key.stringValue, expected);
  } catch (_) {
    return false;
  }
}

bool _equal(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  var diff = 0;
  for (var i = 0; i < a.length; i++) {
    diff |= a[i] ^ b[i];
  }
  return diff == 0;
}

class _PinChecker extends tls.Checker {
  _PinChecker(this.expected);
  final List<int> expected;

  @override
  void call(dynamic connection) {
    final state = connection as tls.TlsConnection;
    final cert = state.session.serverCertChain?.x509List.firstOrNull;
    if (cert == null || !matchesServerCertificate(cert.bytes, expected)) {
      throw tls.TLSAuthenticationError('pinned Ed25519 key mismatch');
    }
  }
}

Future<ConnectionTask<Socket>> openPinnedConnection(
  String host,
  int port,
  List<int> pin,
) async {
  final connect = await Socket.startConnect(host, port);
  Socket? raw;
  var cancelled = false;
  void cancel() {
    cancelled = true;
    connect.cancel();
    raw?.destroy();
  }

  Future<Socket> handshake() async {
    try {
      raw = await connect.socket;
      final socket = raw!;
      if (cancelled) throw const SocketException('Connection cancelled');
      final channel = tls.TlsConnection(socket);
      await channel.handshakeClient(
        settings: tls.HandshakeSettings(
          minVersion: (3, 4),
          maxVersion: (3, 4),
          useHeartbeatExtension: false,
          // Offer a share for every advertised group so Go can select one
          // from the first ClientHello.
          eccCurves: const ['x25519', 'secp256r1'],
          keyShares: const ['x25519', 'secp256r1'],
          pskModes: const [],
        ),
        alpn: const ['http/1.1'],
        checker: _PinChecker(pin),
      );
      return _PinnedSocket(socket, channel);
    } on tls.TLSAuthenticationError {
      cancel();
      throw const PairTrustFailure('跨网服务器公钥不匹配');
    } catch (_) {
      cancel();
      rethrow;
    }
  }

  return ConnectionTask.fromSocket(
    handshake().timeout(
      const Duration(seconds: 6),
      onTimeout: () {
        cancel();
        throw TimeoutException('跨网服务 TLS 握手超时');
      },
    ),
    cancel,
  );
}

class _PinnedSocket extends Stream<Uint8List> implements Socket {
  _PinnedSocket(this._raw, this._tls);

  final Socket _raw;
  final tls.TlsConnection _tls;
  late final _input = _read();
  late final _output = IOSink(_TlsConsumer(_tls, _raw));

  Stream<Uint8List> _read() async* {
    try {
      while (true) {
        final bytes = await _tls.read(max: 16384);
        if (bytes.isEmpty) return;
        yield bytes;
      }
    } on tls.TLSAbruptCloseError {
      // HTTP and WebSocket handle EOF at their message boundaries.
      return;
    } on tls.TLSRemoteAlert catch (error) {
      if (error.description != tls.AlertDescription.closeNotify) {
        throw SocketException('跨网 TLS 连接已中断', address: remoteAddress);
      }
    }
  }

  @override
  StreamSubscription<Uint8List> listen(
    void Function(Uint8List)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) => _input.listen(
    onData,
    onError: onError,
    onDone: onDone,
    cancelOnError: cancelOnError,
  );

  @override
  InternetAddress get address => _raw.address;
  @override
  InternetAddress get remoteAddress => _raw.remoteAddress;
  @override
  int get port => _raw.port;
  @override
  int get remotePort => _raw.remotePort;
  @override
  bool setOption(SocketOption option, bool enabled) =>
      _raw.setOption(option, enabled);
  @override
  Uint8List getRawOption(RawSocketOption option) => _raw.getRawOption(option);
  @override
  void setRawOption(RawSocketOption option) => _raw.setRawOption(option);
  @override
  void destroy() => _raw.destroy();
  @override
  Future<void> close() => _output.close();
  @override
  Future<void> get done => _output.done;
  @override
  Future<void> flush() => _output.flush();
  @override
  void add(List<int> data) => _output.add(data);
  @override
  void addError(Object error, [StackTrace? stackTrace]) =>
      _output.addError(error, stackTrace);
  @override
  Future<void> addStream(Stream<List<int>> stream) => _output.addStream(stream);
  @override
  Encoding get encoding => _output.encoding;
  @override
  set encoding(Encoding value) => _output.encoding = value;
  @override
  void write(Object? value) => _output.write(value);
  @override
  void writeAll(Iterable<dynamic> objects, [String separator = '']) =>
      _output.writeAll(objects, separator);
  @override
  void writeCharCode(int charCode) => _output.writeCharCode(charCode);
  @override
  void writeln([Object? value = '']) => _output.writeln(value);
}

class _TlsConsumer implements StreamConsumer<List<int>> {
  _TlsConsumer(this._tls, this._raw);
  final tls.TlsConnection _tls;
  final Socket _raw;

  @override
  Future<void> addStream(Stream<List<int>> stream) async {
    await for (final bytes in stream) {
      await _tls.write(Uint8List.fromList(bytes));
    }
  }

  @override
  Future<void> close() async {
    try {
      await _tls.close().timeout(const Duration(seconds: 1));
    } finally {
      _raw.destroy();
    }
  }
}
