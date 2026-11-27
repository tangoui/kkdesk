import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/desktop_meta.dart';
import 'package:kkdesk_mobile/lan_session.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/pairing/transport.dart';
import 'package:shared_preferences_platform_interface/in_memory_shared_preferences_async.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_async_platform_interface.dart';
import 'package:shared_preferences_platform_interface/types.dart';

import 'support/webrtc.dart';

Future<void> _frame(TestWebRTC rtc, List<int> bytes) =>
    rtc.meta(Uint8List.fromList(bytes));

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    final previous = SharedPreferencesAsyncPlatform.instance;
    SharedPreferencesAsyncPlatform.instance =
        InMemorySharedPreferencesAsync.empty();
    addTearDown(() => SharedPreferencesAsyncPlatform.instance = previous);
  });

  test(
    'LAN password authentication keeps only post-authentication state',
    () async {
      final rtc = TestWebRTC();
      final preferences = SharedPreferencesAsyncPlatform.instance;
      final store = _HeldIdentityWrite();
      SharedPreferencesAsyncPlatform.instance = store;
      addTearDown(() => SharedPreferencesAsyncPlatform.instance = preferences);
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      final host = await Ed25519().newKeyPairFromSeed(Uint8List(32));
      final key = encodePairBytes((await host.extractPublicKey()).bytes);
      final signature = await Ed25519().sign([
        ...utf8.encode('kkdesk host answer v2\x00'),
        0,
        0,
        0,
        5,
        ...utf8.encode('offer'),
        0,
        0,
        0,
        6,
        ...utf8.encode('answer'),
      ], keyPair: host);
      server.listen((socket) {
        socket.writeln('{"version":2}');
        socket
            .cast<List<int>>()
            .transform(utf8.decoder)
            .transform(const LineSplitter())
            .listen((_) {
              socket.writeln(
                jsonEncode({
                  'sdp': {'type': 'answer', 'sdp': 'answer'},
                  'hostKey': key,
                  'hostSig': encodePairBytes(signature.bytes),
                }),
              );
            });
      });

      final authSent = Completer<void>();
      rtc.onMetaSent = (bytes) {
        if (bytes.first == 0x41) authSent.complete();
      };
      final ended = <String>[];
      final session = LanSession(onStream: (_) {}, onEnded: ended.add);
      addTearDown(session.close);
      final connecting = session.connect('127.0.0.1:${server.port}', 'secret');
      await _until(() => rtc.calls.contains('setRemoteDescription'));
      await rtc.openChannels();
      await authSent.future;
      await _frame(rtc, [0x91, 1]);
      await _frame(rtc, [0x98, 4]);
      await _frame(rtc, [0x9b, 8]);
      expect(await session.setPrivacyScreen(true), isFalse);
      expect(session.hostKey, isNull);
      await _frame(rtc, [0x52, 0]);
      await store.started.future;
      await _frame(rtc, [0x98, 2]);
      expect(session.desktopStatus.lockScreen, LockScreenState.unknown);
      store.release.complete();
      await connecting;
      expect(session.hostKey, key);
      expect(session.desktopStatus.lockScreen, LockScreenState.ready);
      expect(session.desktopStatus.privacyOn, isFalse);
      expect(session.desktopStatus.cursor, RemoteCursor.arrow);
      expect(await session.setPrivacyScreen(true), isTrue);
      expect(rtc.sentMeta, contains(equals([0x90, 1])));
      expect(session.desktopStatus.privacyOn, isFalse);
      await _frame(rtc, [0x91, 2]);
      expect(session.desktopStatus.notice, '等待对方同意…');
      await _frame(rtc, [0x91, 1]);
      expect(session.desktopStatus.privacyOn, isTrue);
      await session.disconnect();
      expect(rtc.sentMeta.last, [0x62]);
    },
  );

  for (final relay in [false, true]) {
    test(
      'paired ${relay ? 'relay' : 'LAN'} uses the admitted desktop meta channel',
      () async {
        final rtc = TestWebRTC();
        final pair = _Pair(relay);
        final ended = <String>[];
        final session = LanSession(onStream: (_) {}, onEnded: ended.add);
        addTearDown(session.close);
        final connecting = session.connectPaired('电脑', (run) => run(pair));
        await pair.offer.future;
        await rtc.openChannels();
        await _frame(rtc, [0x98, 4]);
        expect(await session.lockAndDisconnect(), isFalse);
        expect(session.hostKey, isNull);
        pair.answer.complete({'type': 'answer', 'sdp': 'answer'});
        await _until(() => rtc.calls.contains('setRemoteDescription'));
        await _frame(rtc, [0x98, 2]);
        await rtc.connected();
        await connecting;
        expect(session.hostKey, pair.transcript.hostKey);
        expect(session.relay, relay);
        expect(session.desktopStatus.lockScreen, LockScreenState.ready);
        expect(await session.setLockAfterDisconnect(true), isTrue);
        expect(await session.lockAndDisconnect(), isTrue);
        expect(
          rtc.sentMeta,
          containsAllInOrder([
            equals([0x92, 1]),
            equals([0x94]),
          ]),
        );
        await _frame(rtc, [0x93, 1]);
        expect(session.desktopStatus.lockAfterDisconnect, isTrue);
        await _frame(rtc, [0x95, 7]);
        expect(session.desktopStatus.notice, startsWith('对方电脑锁屏失败'));
        expect(ended, isEmpty);
        await _frame(rtc, [0x95, 1]);
        expect(session.desktopStatus.notice, '对方已锁屏');
        await _frame(rtc, [0x62, ...utf8.encode('对方已锁屏')]);
        expect(ended, ['对方已锁屏']);
        expect(await session.lockAndDisconnect(), isFalse);
      },
    );
  }

  test('files session ignores desktop frames and rejects requests', () async {
    final rtc = TestWebRTC();
    final pair = _Pair(false)
      ..answer.complete({'type': 'answer', 'sdp': 'answer'});
    final session = LanSession(onStream: (_) {}, onEnded: (_) {});
    addTearDown(session.close);
    final connecting = session.connectPaired(
      '电脑',
      (run) => run(pair),
      mode: LanSessionMode.files,
    );
    await pair.offer.future;
    await _until(() => rtc.calls.contains('setRemoteDescription'));
    await rtc.openChannels();
    await rtc.connected();
    await _frame(rtc, [0x91, 1]);
    await _frame(rtc, [0x98, 2]);
    expect(session.desktopStatus.lockScreen, LockScreenState.unknown);
    expect(await session.setPrivacyScreen(true), isFalse);
    expect(await session.lockAndDisconnect(), isFalse);
    expect(rtc.sentMeta.where((bytes) => bytes.first >= 0x90), isEmpty);
    await session.close();
    await connecting;
  });
}

Future<void> _until(bool Function() ready) async {
  for (var attempt = 0; attempt < 100 && !ready(); attempt++) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(ready(), isTrue);
}

final class _HeldIdentityWrite extends InMemorySharedPreferencesAsync {
  _HeldIdentityWrite() : super.empty();
  final started = Completer<void>();
  final release = Completer<void>();

  @override
  Future<bool> setString(
    String key,
    String value,
    SharedPreferencesOptions options,
  ) async {
    started.complete();
    await release.future;
    return super.setString(key, value, options);
  }
}

class _Pair implements AuthenticatedPair {
  _Pair(bool relay) : transport = _Transport(relay);
  @override
  final PairTransport transport;
  @override
  final transcript = PairTranscript(
    purpose: 'connect',
    clientNonce: '',
    hostNonce: '',
    clientKey: '',
    hostKey: encodePairBytes(List.filled(32, 1)),
  );
  final offer = Completer<void>();
  final answer = Completer<PairMessage>();
  @override
  Future<void> send(PairMessage msg) async => offer.complete();
  @override
  Future<PairMessage> receive([
    Duration timeout = const Duration(seconds: 8),
  ]) => answer.future;
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Transport implements PairTransport {
  _Transport(this.relay);
  @override
  final bool relay;
  @override
  bool get relayOnly => relay;
  @override
  List<PairMessage> get ice => const [];
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
