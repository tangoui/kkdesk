import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/desktop_meta.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/remote_unlock.dart';
import 'package:local_auth/local_auth.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const storage = FlutterSecureStorage();
  final key = encodePairBytes(List.filled(32, 1));
  final otherKey = encodePairBytes(List.filled(32, 2));
  late _DeviceAuth auth;
  late List<List<int>> requests;
  late bool allowed;

  RemoteUnlock controller([String? host]) => RemoteUnlock(
    hostKey: () => host ?? key,
    canSend: () => allowed,
    send: (secret) async {
      requests.add(List.of(secret));
      return true;
    },
    changed: () {},
    auth: auth,
    storage: storage,
  );

  setUp(() {
    FlutterSecureStorage.setMockInitialValues({});
    auth = _DeviceAuth();
    requests = [];
    allowed = true;
  });

  test(
    'manual unlock saves only after success and isolates host keys',
    () async {
      final first = controller();
      final second = controller(otherKey);
      addTearDown(first.close);
      addTearDown(second.close);
      expect(await first.requestSaved(), SavedUnlock.manual);
      expect(await first.canRemember(), isTrue);
      final bytes = Uint8List.fromList(utf8.encode(' 测试 '));
      await first.submit(bytes, remember: true);
      expect(requests.single, utf8.encode(' 测试 '));
      expect(await storage.readAll(), isEmpty);
      await first.receive(UnlockResult.succeeded);
      expect(first.message, '对方电脑已解锁');
      expect(await first.hasSaved(), isTrue);
      expect(await second.requestSaved(), SavedUnlock.manual);
      await first.forget();
      expect(await first.hasSaved(), isFalse);
    },
  );

  test(
    'saved unlock uses enrolled biometrics and reports a bad stored password',
    () async {
      final unlock = controller();
      addTearDown(unlock.close);
      await unlock.submit(Uint8List.fromList([0x31]), remember: true);
      await unlock.receive(UnlockResult.succeeded);
      requests.clear();
      auth.answer = false;
      expect(await unlock.requestSaved(), SavedUnlock.finished);
      expect(requests, isEmpty);
      expect(auth.prompts, [true]);
      auth.failure = const LocalAuthException(
        code: LocalAuthExceptionCode.userCanceled,
      );
      await unlock.requestSaved();
      expect(requests, isEmpty);
      expect(auth.prompts, [true, true]);
      auth.failure = null;
      auth.answer = true;
      await unlock.requestSaved();
      expect(requests, [
        [0x31],
      ]);
      await unlock.receive(UnlockResult.stillLocked);
      expect(unlock.savedFailed, isTrue);
      expect(unlock.message, '已保存的解锁密码不正确');
      await unlock.forget();
      expect(unlock.savedFailed, isFalse);
    },
  );

  test('uses the device password with no biometric enrollment', () async {
    auth.enrolled = [];
    final unlock = controller();
    addTearDown(unlock.close);
    expect(await unlock.canRemember(), isTrue);
    await unlock.submit(Uint8List.fromList([0x31]), remember: true);
    await unlock.receive(UnlockResult.succeeded);
    await unlock.requestSaved();
    expect(auth.prompts, [false]);
  });

  test('no phone authentication means manual entry without saving', () async {
    auth.enrolled = [];
    auth.deviceSupported = false;
    final unlock = controller();
    addTearDown(unlock.close);
    expect(await unlock.canRemember(), isFalse);
    await unlock.submit(Uint8List.fromList([0x31]), remember: true);
    await unlock.receive(UnlockResult.succeeded);
    expect(requests, [
      [0x31],
    ]);
    expect(await storage.readAll(), isEmpty);
  });

  test(
    'lock state changes while authenticating prevent a saved submission',
    () async {
      final unlock = controller();
      addTearDown(unlock.close);
      await unlock.submit(Uint8List.fromList([0x31]), remember: true);
      await unlock.receive(UnlockResult.succeeded);
      requests.clear();
      auth.held = Completer<bool>();
      final attempt = unlock.requestSaved();
      await auth.started.future;
      allowed = false;
      auth.held!.complete(true);
      await attempt;
      expect(requests, isEmpty);
    },
  );

  test(
    'handles manual errors, rate limits, and ignores results after close',
    () async {
      final unlock = controller();
      addTearDown(unlock.close);
      final bytes = Uint8List.fromList([0x31]);
      await unlock.submit(bytes, remember: true);
      await unlock.receive(UnlockResult.stillLocked);
      expect(unlock.message, '解锁密码可能不正确');
      expect(await storage.readAll(), isEmpty);
      await unlock.submit(bytes, remember: false);
      await unlock.receive(UnlockResult.unsupported);
      expect(unlock.message, '对方电脑当前无法远程解锁');
      await unlock.submit(bytes, remember: false);
      await unlock.receive(UnlockResult.tooMany);
      expect(unlock.message, '本次连接的解锁尝试次数已达上限');
      await unlock.submit(bytes, remember: true);
      unlock.close();
      await unlock.receive(UnlockResult.succeeded);
      expect(await storage.readAll(), isEmpty);
    },
  );

  testWidgets('timeout clears the remembered password before a late success', (
    tester,
  ) async {
    final unlock = controller();
    addTearDown(unlock.close);
    await unlock.submit(Uint8List.fromList([0x31]), remember: true);
    await tester.pump(const Duration(seconds: 25));
    expect(unlock.message, '等待解锁结果超时');
    await unlock.receive(UnlockResult.succeeded);
    expect(await storage.readAll(), isEmpty);
  });
}

class _DeviceAuth extends LocalAuthentication {
  List<BiometricType> enrolled = [BiometricType.face];
  bool deviceSupported = true;
  bool answer = true;
  LocalAuthException? failure;
  Completer<bool>? held;
  final started = Completer<void>();
  final prompts = <bool>[];

  @override
  Future<List<BiometricType>> getAvailableBiometrics() async => enrolled;

  @override
  Future<bool> isDeviceSupported() async => deviceSupported;

  @override
  Future<bool> authenticate({
    required String localizedReason,
    Iterable<Object> authMessages = const [],
    bool biometricOnly = false,
    bool sensitiveTransaction = true,
    bool persistAcrossBackgrounding = false,
  }) async {
    prompts.add(biometricOnly);
    if (!started.isCompleted) started.complete();
    if (failure case final failure?) throw failure;
    if (held case final held?) return held.future;
    return answer;
  }
}
