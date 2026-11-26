import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/main.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/touch_controller.dart';

class _EndedSession extends ChangeNotifier implements RemoteSession {
  @override
  RemoteStatus get status =>
      const RemoteStatus(phase: RemotePhase.ended, detail: '被控端已断开连接');
  @override
  double get aspectRatio => 16 / 9;
  @override
  Widget video() => const ColoredBox(color: Colors.black);
  @override
  int get screen => 0;
  @override
  List<String> get screens => const ['主屏幕'];
  @override
  RemoteQuality get quality => RemoteQuality.values.first;
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

class _CountingSessions implements RemoteSessions {
  int opened = 0;
  @override
  RemoteSession paired(Computer computer) {
    opened++;
    return _EndedSession();
  }

  @override
  RemoteSession lan(String address, String password) =>
      paired(Computer(id: address, name: address, os: ComputerOS.unknown));
}

void main() {
  testWidgets('ended session shows a compact card and reconnects', (
    tester,
  ) async {
    final demo = MobileServices.demo();
    final sessions = _CountingSessions();
    final services = MobileServices(
      devices: demo.devices,
      pairing: demo.pairing,
      sessions: sessions,
      settings: demo.settings,
      biometrics: demo.biometrics,
      history: demo.history,
      camera: false,
    );
    await tester.binding.setSurfaceSize(const Size(844, 390));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await tester.pumpWidget(KkdeskApp(services: services));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('connect-mac')));
    await tester.pumpAndSettle();
    if (find.text('知道了').evaluate().isNotEmpty) {
      await tester.tap(find.text('知道了'));
      await tester.pumpAndSettle();
    }

    expect(find.text('连接已断开'), findsOneWidget);
    expect(find.textContaining('被控端已断开连接'), findsOneWidget);
    final card = tester.getSize(
      find
          .ancestor(of: find.text('连接已断开'), matching: find.byType(DecoratedBox))
          .first,
    );
    expect(card.width, lessThanOrEqualTo(300));
    expect(sessions.opened, 1);

    await tester.tap(find.byKey(const ValueKey('remote-reconnect')));
    await tester.pumpAndSettle();
    expect(sessions.opened, 2);
    expect(find.text('连接已断开'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('remote-back')));
    await tester.pumpAndSettle();
    expect(find.text('连接已断开'), findsNothing);
    expect(sessions.opened, 2);
  });
}
