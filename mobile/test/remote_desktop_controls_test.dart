import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/desktop_meta.dart';
import 'package:kkdesk_mobile/desktop_status.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/remote_unlock.dart';
import 'package:kkdesk_mobile/touch_controller.dart';
import 'package:kkdesk_mobile/ui/files/transfer_center.dart';
import 'package:kkdesk_mobile/ui/remote.dart';

void main() {
  test('lock screen diagnostics match desktop wording', () {
    const expected = [
      null,
      null,
      '对方电脑已锁屏',
      '对方电脑未安装锁屏远控组件',
      '对方系统暂不支持锁屏远程操作',
      '锁屏远控不可用（需要正式签名的 kkdesk）',
      '对方的 kkdesk 未从安装位置运行，请在对方电脑上从安装位置启动 kkdesk',
      '对方锁屏远控服务未运行或无法连接助手',
      '请在对方电脑上更新锁屏远控组件',
      '对方锁屏远控组件出错，详见对方日志',
      '请在对方电脑的系统设置中批准锁屏远控组件',
    ];
    expect(
      LockScreenState.values.map(
        (state) => DesktopStatus(lockScreen: state).lockReason,
      ),
      expected,
    );
  });

  testWidgets(
    'disconnect controls remain tappable when the session ends with the menu open',
    (tester) async {
      final session = _Desktop();
      await _open(tester, session);
      await _menu(tester);
      await tester.tap(find.text('远控选项'));
      await tester.pumpAndSettle();
      session.phase = RemotePhase.ended;
      session.notify();
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('remote-back')).hitTestable(),
        findsOneWidget,
      );
    },
  );

  testWidgets('host privacy and lock updates drive remote controls', (
    tester,
  ) async {
    final session = _Desktop();
    await _open(tester, session);
    await _menu(tester);
    await tester.tap(find.text('远控选项'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('隐私屏'));
    await tester.pumpAndSettle();
    expect(session.privacyRequests, [true]);
    session.update(
      const PrivacyReply(PrivacyReplyKind.screen, PrivacyStatus.waiting, ''),
    );
    await tester.pump();
    expect(find.text('等待对方同意…'), findsOneWidget);
    session.update(
      const PrivacyReply(PrivacyReplyKind.screen, PrivacyStatus.on, ''),
    );
    await tester.pump();
    await _menu(tester);
    await tester.tap(find.text('远控选项'));
    await tester.pumpAndSettle();
    expect(find.text('✓'), findsOneWidget);
    expect(find.text('解锁电脑…'), findsNothing);
    await tester.tap(find.text('本次断开后锁屏'));
    await tester.pumpAndSettle();
    expect(session.lockRequests, [true]);
    await _menu(tester);
    await tester.tap(find.text('远控选项'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('锁屏并断开'));
    await tester.pumpAndSettle();
    expect(session.lockNowRequests, 1);
  });

  testWidgets(
    'locked without video allows manual unlock with remember unchecked',
    (tester) async {
      final session = _Desktop(phase: RemotePhase.connecting)
        ..update(const LockScreenUpdate(LockScreenState.ready));
      await _open(tester, session);
      expect(find.text('对方电脑已锁屏'), findsOneWidget);
      expect(find.text('请在电脑上点「接受」'), findsNothing);
      await _menu(tester);
      await tester.tap(find.text('远控选项'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('解锁电脑…'));
      await tester.pumpAndSettle();
      expect(find.text('对方电脑的登录密码或 PIN'), findsOneWidget);
      expect(tester.widget<Checkbox>(find.byType(Checkbox)).value, isFalse);
      expect(
        tester.widget<EditableText>(find.byType(EditableText)).obscureText,
        isTrue,
      );
      await tester.enterText(find.byType(EditableText), '1234');
      await tester.tap(find.text('解锁'));
      await tester.pumpAndSettle();
      expect(session.unlock.requests.single.$1, [49, 50, 51, 52]);
      expect(session.unlock.requests.single.$2, isFalse);
    },
  );

  testWidgets('diagnostics disable unlock and saved errors offer recovery', (
    tester,
  ) async {
    final session = _Desktop()
      ..update(const LockScreenUpdate(LockScreenState.componentNeedsUpdate));
    await _open(tester, session);
    expect(find.text('请在对方电脑上更新锁屏远控组件'), findsOneWidget);
    await _menu(tester);
    await tester.tap(find.text('远控选项'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('解锁电脑…'));
    await tester.pumpAndSettle();
    expect(session.unlock.savedRequests, 0);
    session.unlock.failed = true;
    session.update(const LockScreenUpdate(LockScreenState.ready));
    await tester.pump();
    expect(find.text('重新输入'), findsOneWidget);
    expect(find.text('忘记解锁密码'), findsOneWidget);
  });

  testWidgets(
    'view-only hides unlock and sessions without desktop controls hide the panel',
    (tester) async {
      final session = _Desktop()
        ..update(const LockScreenUpdate(LockScreenState.ready));
      await _open(tester, session, viewOnly: true);
      await _menu(tester);
      await tester.tap(find.text('远控选项'));
      await tester.pumpAndSettle();
      expect(find.text('隐私屏'), findsOneWidget);
      expect(find.text('解锁电脑…'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      await _open(tester, _Session());
      await _menu(tester);
      expect(find.text('远控选项'), findsNothing);
    },
  );

  testWidgets('trackpad paints host shape and direct touch hides it', (
    tester,
  ) async {
    final session = _Desktop();
    await _open(tester, session);
    final cursor = find.byKey(const ValueKey('trackpad-cursor'));
    expect(cursor, findsOneWidget);
    session.update(const CursorUpdate(RemoteCursor.pointer));
    await tester.pump();
    expect(
      find.descendant(of: cursor, matching: find.byIcon(Icons.pan_tool_alt)),
      findsOneWidget,
    );
    await _menu(tester);
    await tester.tap(find.byKey(const ValueKey('remote-mode')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('mode-direct')));
    await tester.pumpAndSettle();
    expect(cursor, findsNothing);
  });
}

Future<void> _open(
  WidgetTester tester,
  RemoteSession session, {
  bool viewOnly = false,
}) async {
  await tester.binding.setSurfaceSize(const Size(844, 390));
  addTearDown(() => tester.binding.setSurfaceSize(null));
  final services = MobileServices.demo();
  await services.settings.seeGestures();
  final files = TransferCenter();
  addTearDown(files.dispose);
  await tester.pumpWidget(
    MaterialApp(
      home: RemotePage(
        computer: const Computer(id: 'host', name: '电脑', os: ComputerOS.mac),
        session: session,
        settings: services.settings,
        history: services.history,
        fileCenter: files,
        viewOnly: viewOnly,
      ),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> _menu(WidgetTester tester) async {
  await tester.tap(find.byKey(const ValueKey('remote-ball')));
  await tester.pumpAndSettle();
}

class _Session extends ChangeNotifier implements RemoteSession {
  _Session({this.phase = RemotePhase.connected});
  RemotePhase phase;
  void notify() => notifyListeners();
  @override
  RemoteStatus get status => RemoteStatus(phase: phase);
  @override
  double get aspectRatio => 16 / 9;
  @override
  Widget video() => const SizedBox.expand();
  @override
  int get screen => 0;
  @override
  List<String> get screens => const ['主屏幕'];
  @override
  RemoteQuality get quality => RemoteQuality.auto;
  @override
  bool get canChangeQuality => false;
  @override
  bool get canUseClipboard => false;
  @override
  Future<void> start() async {}
  @override
  void pointer(PointerCommand command, int modifiers) {}
  @override
  void key(int hid, bool down, int modifiers) {}
  @override
  void setScreen(int index) {}
  @override
  void setQuality(RemoteQuality quality) {}
  @override
  Future<String> readClipboard() async => '';
  @override
  Future<void> writeClipboard(String text) async {}
  @override
  Future<void> close() async {}
}

class _Desktop extends _Session implements DesktopRemoteSession {
  _Desktop({super.phase});
  @override
  final _Unlock unlock = _Unlock();
  @override
  bool get desktopReady => true;
  @override
  DesktopStatus desktopStatus = const DesktopStatus();

  final privacyRequests = <bool>[];
  final lockRequests = <bool>[];
  int lockNowRequests = 0;

  void update(DesktopMeta meta) {
    desktopStatus = desktopStatus.apply(meta);
    notifyListeners();
  }

  @override
  Future<bool> setPrivacyScreen(bool on) async {
    privacyRequests.add(on);
    return true;
  }

  @override
  Future<bool> setLockAfterDisconnect(bool on) async {
    lockRequests.add(on);
    return true;
  }

  @override
  Future<bool> lockAndDisconnect() async {
    lockNowRequests++;
    return true;
  }
}

class _Unlock implements RemoteUnlock {
  final requests = <(List<int>, bool)>[];
  int savedRequests = 0;
  bool failed = false;
  @override
  bool get busy => false;
  @override
  String? get message => null;
  @override
  bool get savedFailed => failed;
  @override
  Future<bool> hasSaved() async => failed;
  @override
  Future<bool> canRemember() async => true;
  @override
  Future<SavedUnlock> requestSaved() async {
    savedRequests++;
    return SavedUnlock.manual;
  }

  @override
  Future<void> submit(Uint8List secret, {required bool remember}) async {
    requests.add((List.of(secret), remember));
  }

  @override
  Future<void> forget() async {}
  @override
  Future<void> receive(UnlockResult result) async {}
  @override
  void close() {}
}
