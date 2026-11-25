import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/pairing/biometric_key.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('kkdesk/biometric-key');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const key = BiometricKey();
  const id = 'AAAAAAAAAAAAAAAAAAAAAA';

  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test(
    'prepare forwards the device ID and returns an uncompressed point',
    () async {
      final publicKey = Uint8List(65)..[0] = 4;
      messenger.setMockMethodCallHandler(channel, (call) async {
        expect(call.method, 'prepare');
        expect(call.arguments, {'id': id});
        return publicKey;
      });

      expect(await key.prepare(id), publicKey);
    },
  );

  test(
    'sign forwards the unmodified challenge and returns DER bytes',
    () async {
      final message = Uint8List.fromList([0, 255, 128]);
      final signature = Uint8List.fromList([0x30, 6, 2, 1, 1, 2, 1, 2]);
      messenger.setMockMethodCallHandler(channel, (call) async {
        expect(call.method, 'sign');
        expect(call.arguments, {'id': id, 'message': message});
        return signature;
      });

      expect(await key.sign(id, message), signature);
    },
  );

  test('remove forwards the device ID', () async {
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'remove');
      expect(call.arguments, {'id': id});
      return null;
    });

    await key.remove(id);
  });

  for (final code in ['invalidated', 'missing', 'cancelled', 'unavailable']) {
    test('preserves the native $code error', () async {
      messenger.setMockMethodCallHandler(channel, (_) async {
        throw PlatformException(code: code);
      });

      await expectLater(
        key.sign(id, Uint8List(1)),
        throwsA(isA<PlatformException>().having((e) => e.code, 'code', code)),
      );
    });
  }

  test(
    'reports unavailable when the platform has no biometric bridge',
    () async {
      await expectLater(
        key.prepare(id),
        throwsA(
          isA<PlatformException>().having((e) => e.code, 'code', 'unavailable'),
        ),
      );
    },
  );

  test('rejects malformed public keys and absent signatures', () async {
    messenger.setMockMethodCallHandler(channel, (call) async {
      return call.method == 'prepare' ? Uint8List(65) : null;
    });

    await expectLater(key.prepare(id), throwsA(isA<PlatformException>()));
    await expectLater(
      key.sign(id, Uint8List(1)),
      throwsA(isA<PlatformException>()),
    );
  });
}
