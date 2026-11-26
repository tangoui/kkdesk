import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/pairing/services.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  for (final status in ['offline', 'ok', 'unpaired', 'save_failed']) {
    test(
      'unpair $status clears only this computer and its P-256 key',
      () async {
        FlutterSecureStorage.setMockInitialValues({});
        final credentials = PairCredentials();
        final identity = await credentials.key();
        final removedKeys = <String>[];
        const channel = MethodChannel('kkdesk/biometric-key');
        final messenger =
            TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
        messenger.setMockMethodCallHandler(channel, (call) async {
          expect(call.method, 'remove');
          removedKeys.add(call.arguments['id'] as String);
          return null;
        });
        addTearDown(() => messenger.setMockMethodCallHandler(channel, null));

        final host = await Ed25519().newKeyPairFromSeed(Uint8List(32));
        final public = (await host.extractPublicKey()).bytes;
        final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
        final address = '127.0.0.1:${server.port}';
        addTearDown(server.close);
        var notified = false;
        if (status == 'offline') {
          await server.close();
        } else {
          server.listen((socket) async {
            final frames = StreamIterator(
              socket
                  .cast<List<int>>()
                  .transform(utf8.decoder)
                  .transform(const LineSplitter()),
            );
            Future<PairMessage> read() async {
              expect(await frames.moveNext(), isTrue);
              return (jsonDecode(frames.current) as PairMessage)['mobile']
                  as PairMessage;
            }

            void send(PairMessage message) =>
                socket.writeln(jsonEncode({'mobile': message}));

            try {
              socket.writeln('{"nonce":"legacy"}');
              final hello = await read();
              expect(hello['purpose'], 'unpair');
              final transcript = PairTranscript(
                purpose: 'unpair',
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
                }),
              );
              await socket.flush();
              final request = await read();
              expect(request['type'], 'request');
              expect(
                await transcript.verify(transcript.clientKey, request),
                isTrue,
              );
              expect(removedKeys, isEmpty);
              notified = true;
              send(
                await transcript.sign(host, {
                  'type': 'result',
                  'status': status,
                }),
              );
              await socket.flush();
            } finally {
              socket.destroy();
              await frames.cancel();
            }
          });
        }

        final target = PairTarget(
          id: desktopID(public),
          code: '',
          fingerprint: fingerprint(public),
          lan: [address],
        );
        final other = PairTarget(
          id: desktopID(Uint8List(32)),
          code: '',
          fingerprint: fingerprint(Uint8List(32)),
          lan: const [],
        );
        final devices = PairedDevices(PairNetwork(credentials));
        addTearDown(devices.dispose);
        await devices.savePair(
          Computer(id: target.id, name: '测试电脑', os: ComputerOS.mac),
          target,
        );
        await devices.savePair(
          Computer(id: other.id, name: '另一台', os: ComputerOS.windows),
          other,
        );

        final result = await devices.remove(target.id);

        expect(
          result,
          status == 'offline' || status == 'save_failed'
              ? UnpairResult.localOnly
              : UnpairResult.removed,
        );
        expect(notified, status != 'offline');
        expect(removedKeys, [target.id]);
        expect(devices.target(target.id), isNull);
        expect(devices.computers.single.id, other.id);
        final loaded = PairedDevices(PairNetwork(PairCredentials()));
        addTearDown(loaded.dispose);
        await loaded.load(poll: false);
        expect(loaded.target(target.id), isNull);
        expect(loaded.computers.single.id, other.id);
        expect(
          await loaded.network.credentials.key().then(
            (key) => key.extractPublicKey(),
          ),
          await identity.extractPublicKey(),
        );
      },
    );
  }
}
