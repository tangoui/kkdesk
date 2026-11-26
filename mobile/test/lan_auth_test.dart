import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/lan_auth.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/ui/common.dart';
import 'package:shared_preferences/shared_preferences.dart';
// The platform interface is already used by the existing session tests.
// ignore: depend_on_referenced_packages
import 'package:shared_preferences_platform_interface/in_memory_shared_preferences_async.dart';
// ignore: depend_on_referenced_packages
import 'package:shared_preferences_platform_interface/shared_preferences_async_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final vector = (jsonDecode(
    File('../internal/session/testdata/auth-v2.json').readAsStringSync(),
  ) as Map<String, dynamic>).cast<String, String>();

  test(
    'validates the Go signature and password proof on the exact SDP',
    () async {
      final offer = vector['offer']!;
      final answer = vector['answer']!;
      final key = vector['hostKey']!;
      final signature = vector['hostSig']!;
      expect(await verifyLanAnswer(offer, answer, key, signature), key);
      expect(hostDeviceCode(key), vector['code']);
      final proof = lanPasswordProof(vector['password']!, offer, answer);
      expect(proof.first, 0x41);
      expect(encodePairBytes(proof.sublist(1)), vector['proof']);
      for (final (o, a, k, sig) in [
        ('$offer ', answer, key, signature),
        (offer, '$answer ', key, signature),
        (offer, answer, key, null),
        (offer, answer, encodePairBytes(List.filled(32, 0)), signature),
      ]) {
        await expectLater(
          verifyLanAnswer(o, a, k, sig),
          throwsA(isA<LanAuthFailure>()),
        );
      }
    },
  );

  test('v2 greeting and auth result reject missing or malformed frames', () {
    expect(
      () => requireLanV2({'nonce': 'legacy'}),
      throwsA(isA<LanAuthFailure>()),
    );
    requireLanV2({'version': 2});
    final success = LanAuthResult.decode([0x52, 0]);
    expect(success, isNotNull);
    expect(success?.failure, isNull);
    expect(LanAuthResult.decode([0x52, 1])?.failure, '密码错误');
    expect(
      LanAuthResult.decode([0x52, 2, 0, 0, 0, 30])?.failure,
      contains('30 秒'),
    );
    expect(LanAuthResult.decode([0x52, 2, 0, 0, 0, 0]), isNull);
    expect(LanAuthResult.decode([0x52, 2]), isNull);
    expect(LanAuthResult.decode([0x61, 0]), isNull);
  });

  test(
    'persists the first key by normalized address and can explicitly forget it',
    () async {
      final previous = SharedPreferencesAsyncPlatform.instance;
      SharedPreferencesAsyncPlatform.instance =
          InMemorySharedPreferencesAsync.empty();
      addTearDown(() => SharedPreferencesAsyncPlatform.instance = previous);
      final prefs = SharedPreferencesAsync();
      final endpoint = passwordLanEndpoint('TEST.local');
      expect(endpoint, passwordLanEndpoint('test.local:21118'));
      final store = LanIdentities(prefs);
      final key = vector['hostKey']!;
      final different = encodePairBytes(List.filled(32, 0));
      await store.check(endpoint, key);
      expect(await store.remember(endpoint, key), vector['code']);
      final reopened = LanIdentities(SharedPreferencesAsync());
      expect(await reopened.remember(endpoint, key), isNull);
      await expectLater(
        reopened.check(endpoint, different),
        throwsA(isA<LanIdentityChanged>()),
      );
      try {
        await reopened.remember(endpoint, different);
        fail('competing connection replaced the saved identity');
      } on LanIdentityChanged catch (error) {
        await error.forget();
      }
      await reopened.check(endpoint, different);
      expect(
        await reopened.remember(endpoint, different),
        hostDeviceCode(different),
      );
      await reopened.forget(endpoint);
    },
  );

  testWidgets('forgetting the LAN identity requires confirmation', (
    tester,
  ) async {
    final previous = SharedPreferencesAsyncPlatform.instance;
    SharedPreferencesAsyncPlatform.instance =
        InMemorySharedPreferencesAsync.empty();
    addTearDown(() => SharedPreferencesAsyncPlatform.instance = previous);
    final store = LanIdentities();
    final endpoint = passwordLanEndpoint('local.test');
    final key = vector['hostKey']!;
    final different = encodePairBytes(List.filled(32, 0));
    late LanIdentityChanged changed;
    await tester.runAsync(() async {
      await store.remember(endpoint, key);
      try {
        await store.check(endpoint, different);
        fail('changed identity did not raise an error');
      } on LanIdentityChanged catch (error) {
        changed = error;
      }
    });
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => confirmForgetLanIdentity(context, changed),
              child: const Text('忘记这台电脑的身份'),
            ),
          ),
        ),
      ),
    );
    final forget = find.text('忘记这台电脑的身份');
    await tester.tap(forget);
    await tester.pumpAndSettle();
    expect(find.textContaining('只有确认对方重装'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    await tester.runAsync(
      () => expectLater(
        store.check(endpoint, different),
        throwsA(isA<LanIdentityChanged>()),
      ),
    );

    await tester.tap(forget);
    await tester.pumpAndSettle();
    await tester.tap(find.text('忘记身份'));
    await tester.pumpAndSettle();
    await tester.runAsync(() => store.check(endpoint, different));
  });
}
