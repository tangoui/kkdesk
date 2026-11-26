import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart' hide Hmac;
import 'package:crypto/crypto.dart';
import 'package:tlslite/tlslite.dart' as tls;
// tlslite exposes its server API, but keeps the PEM key parser internal.
// ignore: implementation_imports
import 'package:tlslite/src/utils/keyfactory.dart' as keys;
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/pairing/pinned_tls.dart';
import 'package:kkdesk_mobile/pairing/services.dart';
import 'package:kkdesk_mobile/pairing/transport.dart';

String fixture(String path) =>
    File('test/fixtures/pairing/$path').readAsStringSync().trim();
PairMessage data(String name) =>
    jsonDecode(fixture('$name.json')) as PairMessage;

void main() {
  test('repeat QR works offline with the saved identity', () async {
    FlutterSecureStorage.setMockInitialValues({});
    final devices = PairedDevices(PairNetwork(PairCredentials()));
    addTearDown(devices.dispose);
    final input = Uri.parse(fixture('uri.txt'));
    final target = PairTarget.parse(input.toString());
    final hostKey = data('values')['hostKey'] as String;
    await devices.savePair(
      Computer(id: target.id, name: '已有的电脑', os: ComputerOS.mac),
      target.verified(hostKey),
    );
    final service = RealPairingService(
      devices,
      phoneInfo: () async => throw StateError('repeat QR requested phone info'),
    );
    final request = await service.begin(input.toString());
    expect(request.computer.name, '已有的电脑');
    expect(request.verificationCode, isEmpty);
    expect((await request.statuses.single).name, 'alreadyPaired');
    expect(devices.computers, hasLength(1));
    expect(devices.target(target.id)?.code, isEmpty);
  });

  test(
    'repeat QR updates only authenticated metadata without claiming the invite',
    () async {
      FlutterSecureStorage.setMockInitialValues({});
      final host = await Ed25519().newKeyPairFromSeed(Uint8List(32));
      final public = (await host.extractPublicKey()).bytes;
      final id = desktopID(public);
      final hostKey = encodePairBytes(public);
      final fingerprintValue = fingerprint(public);
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      final exchange = Completer<void>();
      server.listen((socket) async {
        try {
          socket.writeln('{"version":2}');
          final frames = StreamIterator(
            socket
                .cast<List<int>>()
                .transform(utf8.decoder)
                .transform(const LineSplitter()),
          );
          try {
            expect(await frames.moveNext(), isTrue);
            final hello =
                (jsonDecode(frames.current) as PairMessage)['mobile']
                    as PairMessage;
            final tx = PairTranscript(
              purpose: 'pair',
              clientNonce: hello['nonce'] as String,
              hostNonce: encodePairBytes(randomPairBytes(32)),
              clientKey: hello['publicKey'] as String,
              hostKey: hostKey,
            );
            final challenge = await tx.sign(host, {
              'type': 'challenge',
              'publicKey': hostKey,
              'nonce': tx.hostNonce,
              'name': '新电脑名称',
              'os': 'darwin',
            });
            socket.writeln(jsonEncode({'mobile': challenge}));
            await socket.flush();
            expect(await frames.moveNext(), isFalse);
            exchange.complete();
          } finally {
            await frames.cancel();
          }
        } catch (error, trace) {
          exchange.completeError(error, trace);
        } finally {
          socket.destroy();
        }
      });
      final address = '127.0.0.1:${server.port}';
      final input = Uri(
        scheme: 'kkdesk',
        host: 'pair',
        queryParameters: {
          'v': '1',
          'id': id,
          'fp': fingerprintValue,
          'c': 'T5PM62LR52NUFR43D3VCXWRTXA',
          'lan': address,
          's': 'https://example.test',
          't': 'test-server-token-with-32-characters',
        },
      );
      final devices = PairedDevices(PairNetwork(PairCredentials()));
      addTearDown(devices.dispose);
      final oldTarget = PairTarget(
        id: id,
        code: '',
        fingerprint: fingerprintValue,
        lan: const ['127.0.0.1:1'],
        hostKey: hostKey,
      );
      await devices.savePair(
        Computer(id: id, name: '旧电脑名称', os: ComputerOS.mac),
        oldTarget,
      );
      final service = RealPairingService(devices);
      expect(
        await (await service.begin(input.toString())).statuses.single,
        PairStatus.alreadyPaired,
      );
      await exchange.future.timeout(const Duration(seconds: 4));
      expect(devices.computers.single.name, '新电脑名称');
      expect(devices.target(id)?.server, 'https://example.test');
      expect(devices.target(id)?.lan, [address]);
      expect(devices.target(id)?.code, isEmpty);
    },
  );

  test(
    'matches Go QR, verification code and signatures byte for byte',
    () async {
      final target = PairTarget.parse(fixture('uri.txt'));
      final values = data('values');
      final host = decodePairBytes(values['hostKey'] as String, 32);
      final phone = decodePairBytes(values['clientKey'] as String, 32);
      expect(target.id, desktopID(host));
      expect(target.fingerprint, fingerprint(host));
      expect(target.lan, ['192.168.1.99:21118', '[fd00::1]:21118']);
      expect(target.token, 'test-only-token+with-percent-%-and-colon:');
      expect(target.serverKey, values['serverPin']);
      expect(
        verificationCode(target.code, host, phone),
        values['verification'],
      );
      final tx = PairTranscript(
        purpose: 'pair',
        clientNonce: values['clientNonce'] as String,
        hostNonce: values['hostNonce'] as String,
        clientKey: values['clientKey'] as String,
        hostKey: values['hostKey'] as String,
      );
      expect(
        encodePairBytes(tx.bytes(data('challenge'))),
        values['challengeBytes'],
      );
      expect(await tx.verify(tx.hostKey, data('challenge')), isTrue);
      expect(await tx.verify(tx.hostKey, data('pending')), isTrue);
      final key = await Ed25519().newKeyPairFromSeed(
        decodePairBytes(values['phoneSeed'] as String, 32),
      );
      final msg = await tx.sign(key, {
        'type': 'request',
        'code': tx.codeProof(target.code),
        'name': '测试 iPhone',
        'model': 'iPhone 12 Pro',
      });
      expect(
        utf8.encode(jsonEncode(msg)),
        utf8.encode(fixture('request.json')),
      );
      final connect = PairTranscript(
        purpose: 'connect',
        clientNonce: tx.clientNonce,
        hostNonce: values['connectNonce'] as String,
        clientKey: tx.clientKey,
        hostKey: tx.hostKey,
      );
      final offer = await connect.sign(key, {
        'type': 'offer',
        'mode': 'desktop',
        'sdp': 'v=0\r\na=ice-pwd:example\r\n',
      });
      expect(encodePairBytes(connect.bytes(offer)), values['offerBytes']);
      expect(
        utf8.encode(jsonEncode(offer)),
        utf8.encode(fixture('offer.json')),
      );
      offer['sdp'] = 'replaced';
      expect(await connect.verify(connect.clientKey, offer), isFalse);
    },
  );

  test(
    'secure storage retains phone key and renamed paired computer',
    () async {
      FlutterSecureStorage.setMockInitialValues({});
      final store = PairCredentials();
      final key = (await (await store.key()).extractPublicKey());
      final second = (await (await PairCredentials().key()).extractPublicKey());
      expect(second.bytes, key.bytes);
      final devices = PairedDevices(PairNetwork(store));
      addTearDown(devices.dispose);
      final target = PairTarget.parse(fixture('uri.txt'));
      await devices.savePair(
        Computer(id: target.id, name: 'Mac', os: ComputerOS.mac),
        target,
      );
      await devices.rename(target.id, '客厅的 Mac');
      final loaded = PairedDevices(PairNetwork(PairCredentials()));
      addTearDown(loaded.dispose);
      await loaded.load(poll: false);
      expect(loaded.computers.single.name, '客厅的 Mac');
      expect(loaded.target(target.id)?.serverKey, target.serverKey);
      expect(loaded.target(target.id)?.code, isEmpty);
    },
  );

  test(
    'WSS checks the actual Ed25519 certificate before sending the bearer token',
    () async {
      final cert = fixture('server.pem');
      final key = fixture('server-key.pem');
      final der = base64.decode(
        cert.split('\n').where((line) => !line.startsWith('-')).join(),
      );
      final pin = data('values')['serverPin'] as String;
      expect(matchesServerCertificate(der, base64.decode(pin)), isTrue);
      expect(matchesServerCertificate(der, Uint8List(32)), isFalse);
      expect(
        matchesServerCertificate(Uint8List(2), base64.decode(pin)),
        isFalse,
      );
      final chain = tls.X509CertChain()..parsePemList(cert);
      final privateKey = keys.parsePrivateKey(key);
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      var hits = 0;
      server.listen((socket) async {
        try {
          final channel = tls.TlsConnection(socket);
          await channel.handshakeServer(
            settings: tls.HandshakeSettings(
              minVersion: (3, 4),
              maxVersion: (3, 4),
            ),
            certChain: chain,
            privateKey: privateKey,
          );
          final headers = StringBuffer();
          while (!headers.toString().contains('\r\n\r\n')) {
            final bytes = await channel.read(max: 4096);
            if (bytes.isEmpty) return;
            headers.write(ascii.decode(bytes));
          }
          hits++;
          final request = headers.toString();
          expect(
            request.toLowerCase(),
            contains('authorization: bearer test-only-shared-token-1234567890'),
          );
          final nonce = RegExp(
            r'^sec-websocket-key: ([^\r]+)',
            caseSensitive: false,
            multiLine: true,
          ).firstMatch(request)!.group(1)!;
          final accept = base64.encode(
            sha1
                .convert(
                  ascii.encode(
                    '$nonce'
                    '258EAFA5-E914-47DA-95CA-C5AB0DC85B11',
                  ),
                )
                .bytes,
          );
          await channel.write(
            Uint8List.fromList(
              ascii.encode(
                'HTTP/1.1 101 Switching Protocols\r\n'
                'Upgrade: websocket\r\nConnection: Upgrade\r\n'
                'Sec-WebSocket-Accept: $accept\r\n\r\n',
              ),
            ),
          );
          final ready = ascii.encode('{"type":"mobile_ready"}');
          await channel.write(
            Uint8List.fromList([0x81, ready.length, ...ready]),
          );
          await channel.read(max: 4096);
        } on tls.TLSError {
          // A mismatched pin closes the connection during the handshake.
        } finally {
          socket.destroy();
        }
      });
      final url = Uri.https('127.0.0.1:${server.port}').toString();
      PairTarget target(String pin) => PairTarget(
        id: 'AAAAAAAAAAAAAAAAAAAAAA',
        code: '',
        fingerprint: '',
        lan: const [],
        server: url,
        token: 'test-only-shared-token-1234567890',
        serverKey: pin,
      );
      final accepted = await PairTransport.server(target(pin), 'pair');
      await accepted.close();
      await expectLater(
        PairTransport.server(target(base64.encode(Uint8List(32))), 'pair'),
        throwsA(isA<PairTrustFailure>()),
      );
      expect(hits, 1);
    },
  );

  for (final outcome in [
    'ok',
    'rejected',
    'expired',
    'manual',
    'manual-wrong',
    'lost-commit',
    'recovered',
    'manual-recovered',
    'wrong-recovery-nonce',
    'already_paired',
    'manual-already-paired',
    'busy',
    'invalid',
    'future_status',
  ]) {
    test(
      'signed LAN pairing state $outcome saves only an accepted computer',
      () async {
        FlutterSecureStorage.setMockInitialValues({});
        final host = await Ed25519().newKeyPairFromSeed(
          Uint8List.fromList(List.filled(32, 17)),
        );
        final public = ((await host.extractPublicKey())).bytes;
        final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
        addTearDown(server.close);
        final manual = outcome.startsWith('manual');
        final alreadyPaired = outcome == 'manual-already-paired';
        final recovered = outcome.endsWith('recovered');
        final wrongRecovery = outcome == 'wrong-recovery-nonce';
        final success = outcome == 'ok' || outcome == 'manual';
        final lostCommit = outcome == 'lost-commit';
        final invite = manual
            ? 'ABCD2345EFGH6723'
            : 'T5PM62LR52NUFR43D3VCXWRTXA';
        final pairDone = Completer<void>();
        server.listen((socket) async {
          final frames = StreamIterator(
            socket
                .cast<List<int>>()
                .transform(utf8.decoder)
                .transform(const LineSplitter()),
          );
          void send(PairMessage m) => socket.writeln(jsonEncode({'mobile': m}));
          Future<PairMessage> read() async {
            if (!await frames.moveNext()) throw const SocketException('closed');
            return (jsonDecode(frames.current) as PairMessage)['mobile']
                as PairMessage;
          }

          try {
            socket.writeln('{"version":2}');
            final hello = await read();
            final transcript = PairTranscript(
              purpose: hello['purpose'] as String,
              clientNonce: hello['nonce'] as String,
              hostNonce: encodePairBytes(randomPairBytes(32)),
              clientKey: hello['publicKey'] as String,
              hostKey: encodePairBytes(public),
            );
            send(
              await transcript.sign(host, {
                'type': 'challenge',
                'publicKey': transcript.hostKey,
                'nonce': transcript.hostNonce,
                'name': '测试电脑',
                'os': 'darwin',
                if (transcript.purpose == 'pair_manual')
                  'code': encodePairBytes(
                    Hmac(sha256, utf8.encode(invite))
                        .convert(transcript.bytes({'type': 'pair-manual-host'}))
                        .bytes,
                  ),
              }),
            );
            await socket.flush();
            if (outcome == 'manual-wrong' || alreadyPaired) {
              expect(await frames.moveNext(), isFalse);
              pairDone.complete();
              return;
            }
            final request = await read();
            expect(
              await transcript.verify(transcript.clientKey, request),
              isTrue,
            );
            if (transcript.purpose == 'status') {
              send(
                await transcript.sign(host, {'type': 'result', 'status': 'ok'}),
              );
              await socket.flush();
              return;
            }
            expect(jsonEncode(request), isNot(contains(invite)));
            expect(request['code'], transcript.codeProof(invite));
            if (outcome != 'expired' &&
                !alreadyPaired &&
                !recovered &&
                !wrongRecovery) {
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': 'pending',
                  'code': verificationCode(
                    invite,
                    public,
                    decodePairBytes(transcript.clientKey, 32),
                  ),
                }),
              );
            }
            if (success || lostCommit) {
              final nonce = encodePairBytes(randomPairBytes(32));
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': 'approved',
                  'nonce': nonce,
                }),
              );
              await socket.flush();
              final ack = await read();
              expect(ack['type'], 'pair_ack');
              expect(ack['nonce'], nonce);
              expect(
                await transcript.verify(transcript.clientKey, ack),
                isTrue,
              );
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': 'ok',
                  'nonce': nonce,
                }),
              );
              await socket.flush();
              final received = await read();
              expect(received['type'], 'pair_received');
              expect(received['nonce'], nonce);
              expect(
                await transcript.verify(transcript.clientKey, received),
                isTrue,
              );
              if (!lostCommit) {
                send(
                  await transcript.sign(host, {
                    'type': 'result',
                    'status': 'committed',
                    'nonce': nonce,
                  }),
                );
                await socket.flush();
              }
            } else if (recovered || wrongRecovery) {
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': 'committed',
                  'nonce': recovered
                      ? transcript.hostNonce
                      : encodePairBytes(randomPairBytes(32)),
                  'bypass': true,
                }),
              );
              await socket.flush();
            } else {
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': alreadyPaired ? 'already_paired' : outcome,
                }),
              );
              await socket.flush();
            }
            pairDone.complete();
          } finally {
            socket.destroy();
            await frames.cancel();
          }
        });
        final devices = PairedDevices(PairNetwork(PairCredentials()));
        addTearDown(devices.dispose);
        if (alreadyPaired) {
          final pairedTarget = PairTarget(
            id: desktopID(public),
            fingerprint: fingerprint(public),
            code: '',
            lan: const [],
            server: 'https://existing.example.test',
            token: 'existing-server-token-with-32-characters',
            hostKey: encodePairBytes(public),
          );
          await devices.savePair(
            Computer(id: pairedTarget.id, name: '旧名称', os: ComputerOS.mac),
            pairedTarget,
          );
        }
        final service = RealPairingService(
          devices,
          phoneInfo: () async => (name: '手机', model: '测试'),
        );
        final input = Uri(
          scheme: 'kkdesk',
          host: manual ? 'manual' : 'pair',
          queryParameters: {
            if (!manual) ...{
              'v': '1',
              'id': desktopID(public),
              'fp': fingerprint(public),
            },
            'c': !manual
                ? invite
                : outcome == 'manual-wrong'
                ? 'AAAA-2345-EFGH-6723'
                : 'ABCD-2345-EFGH-6723',
            'lan': '127.0.0.1:${server.port}',
          },
        ).toString();
        if (outcome == 'manual-wrong') {
          await expectLater(
            service.begin(input),
            throwsA(isA<PairTrustFailure>()),
          );
        } else {
          final request = await service.begin(input);
          if (recovered) {
            expect(await request.statuses.single, PairStatus.success);
            expect(devices.computers.single.confirmOnHost, isFalse);
          } else if (wrongRecovery) {
            await expectLater(
              request.statuses.toList(),
              throwsA(isA<PairTrustFailure>()),
            );
          } else if (lostCommit) {
            await expectLater(
              request.statuses.toList(),
              throwsA(isA<SocketException>()),
            );
          } else if (alreadyPaired) {
            expect(await request.statuses.single, PairStatus.alreadyPaired);
            expect(devices.computers.single.name, '测试电脑');
            expect(
              devices.target(desktopID(public))?.server,
              'https://existing.example.test',
            );
          } else if (const {
                'already_paired': '配对失败（already_paired）',
                'busy': '电脑正在处理另一个配对请求，请稍后再试',
                'invalid': '手机信息无效',
                'future_status': '配对失败（future_status）',
              }[outcome]
              case final message?) {
            await expectLater(
              request.statuses.toList(),
              throwsA(
                isA<PairFailure>().having((e) => e.message, 'message', message),
              ),
            );
          } else {
            expect((await request.statuses.toList()).last, switch (outcome) {
              'ok' || 'manual' => PairStatus.success,
              'expired' => PairStatus.expired,
              _ => PairStatus.rejected,
            });
          }
        }
        await pairDone.future.timeout(const Duration(seconds: 5));
        expect(
          devices.computers,
          hasLength(success || alreadyPaired || recovered ? 1 : 0),
        );
        if (success) {
          expect(devices.computers.single.id, desktopID(public));
        }
      },
    );
  }
}
