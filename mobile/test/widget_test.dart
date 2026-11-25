import 'dart:ui' show PointerDeviceKind;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/main.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/touch_controller.dart';

Future<void> openApp(WidgetTester tester) async {
  await tester.binding.setSurfaceSize(const Size(390, 844));
  addTearDown(() => tester.binding.setSurfaceSize(null));
  await tester.pumpWidget(KkdeskApp(services: MobileServices.demo()));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('devices are grouped, searchable and provide LAN sign-in', (
    tester,
  ) async {
    await openApp(tester);
    expect(find.text('在线 · 2'), findsOneWidget);
    expect(find.text('离线 · 2'), findsOneWidget);
    expect(find.text('传输'), findsNothing);
    await tester.enterText(find.byType(TextField), '办公室');
    await tester.pumpAndSettle();
    expect(find.text('办公室台式机'), findsOneWidget);
    expect(find.text('渲染服务器'), findsNothing);
    await tester.tap(find.byTooltip('添加电脑'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('局域网地址 + 密码'));
    await tester.pumpAndSettle();
    expect(find.text('电脑地址'), findsOneWidget);
    expect(find.text('连接密码'), findsOneWidget);
  });

  testWidgets('scanner code opens a verification sheet and reports success', (
    tester,
  ) async {
    await openApp(tester);
    await tester.tap(find.byTooltip('扫码配对'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('输入配对码'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('pair-code')), '12345678');
    await tester.pump();
    await tester.tap(find.text('继续'));
    await tester.pump();
    for (var i = 0; i < 10 && find.text('核对码').evaluate().isEmpty; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(find.text('核对码'), findsOneWidget);
    expect(find.text('等待电脑确认…'), findsOneWidget);
    await tester.pump(const Duration(seconds: 4));
    await tester.pump();
    expect(find.text('配对成功'), findsOneWidget);
    await tester.tap(find.text('完成'));
    await tester.pumpAndSettle();
    expect(find.text('已配对'), findsOneWidget);
  });

  testWidgets('settings can switch theme and manage paired devices', (
    tester,
  ) async {
    await openApp(tester);
    await tester.tap(find.text('我的'));
    await tester.pumpAndSettle();
    expect(find.text('App 锁'), findsOneWidget);
    await tester.tap(find.text('主题'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('深色'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<MaterialApp>(find.byType(MaterialApp)).themeMode,
      ThemeMode.dark,
    );
    await tester.tap(find.text('已配对电脑'));
    await tester.pumpAndSettle();
    expect(find.text('更改名称'), findsWidgets);
    expect(find.text('解除配对'), findsWidgets);
  });
  testWidgets('remote tools switch and remember the mode and hold Ctrl', (
    tester,
  ) async {
    final services = MobileServices.demo();
    await tester.binding.setSurfaceSize(const Size(844, 390));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await tester.pumpWidget(KkdeskApp(services: services));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('connect-mac')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pumpAndSettle();
    expect(find.text('远程手势'), findsOneWidget);
    await tester.tap(find.text('知道了'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('trackpad-cursor')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('remote-ball')));
    await tester.pumpAndSettle();
    expect(find.text('18 ms · 直连 · 60 帧'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('remote-mode')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('mode-direct')));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('trackpad-cursor')), findsNothing);
    expect(services.settings.touchMode('mac'), TouchMode.direct);
    expect(services.settings.touchMode('office'), TouchMode.trackpad);

    await tester.tap(find.byKey(const ValueKey('remote-ball')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('remote-keyboard')));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('shortcut-bar')), findsOneWidget);
    final input = tester.widget<EditableText>(find.byType(EditableText).last);
    expect(input.focusNode.hasFocus, isTrue);
    addTearDown(tester.view.resetViewInsets);
    tester.view.viewInsets = const FakeViewPadding(bottom: 100);
    await tester.pump();
    tester.view.viewInsets = const FakeViewPadding();
    await tester.pump(const Duration(milliseconds: 90));
    tester.view.viewInsets = const FakeViewPadding(bottom: 100);
    await tester.pump();
    expect(find.byKey(const ValueKey('shortcut-bar')), findsOneWidget);
    await tester.tap(find.text('Ctrl'), kind: PointerDeviceKind.mouse);
    await tester.pumpAndSettle();
    expect(input.focusNode.hasFocus, isTrue);
    final semantics = find
        .ancestor(of: find.text('Ctrl'), matching: find.byType(Semantics))
        .evaluate()
        .map((element) => element.widget)
        .whereType<Semantics>();
    expect(
      semantics.any((widget) => widget.properties.selected == true),
      isTrue,
    );
    await tester.tap(find.byTooltip('收起键盘'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('shortcut-bar')), findsNothing);

    await tester.tap(find.byKey(const ValueKey('remote-ball')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('remote-disconnect')));
    await tester.pumpAndSettle();
    expect(find.byTooltip('扫码配对'), findsOneWidget);
  });

  testWidgets(
    'biometric failure blocks a paired computer with confirmation disabled',
    (tester) async {
      final biometrics = _RejectBiometrics();
      final services = MobileServices.demo(biometrics: biometrics);
      await tester.pumpWidget(KkdeskApp(services: services));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('connect-office')));
      await tester.pumpAndSettle();
      expect(biometrics.reasons.single, contains('办公室台式机'));
      expect(find.textContaining('生物识别未通过'), findsOneWidget);
      expect(find.byKey(const ValueKey('remote-ball')), findsNothing);
    },
  );
}

class _RejectBiometrics implements Biometrics {
  final reasons = <String>[];

  @override
  Future<bool> authenticate(String reason) async {
    reasons.add(reason);
    return false;
  }
}
