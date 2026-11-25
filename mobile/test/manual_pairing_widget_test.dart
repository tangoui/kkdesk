import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/main.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart' show PairTarget;
import 'package:kkdesk_mobile/ui/common.dart';

void main() {
  testWidgets('manual pairing formats the code and hands a LAN URI to scan', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final pairing = _RecordingPairingService();
    final demo = MobileServices.demo();
    await tester.pumpWidget(
      KkdeskApp(
        services: MobileServices(
          devices: demo.devices,
          pairing: pairing,
          sessions: demo.sessions,
          settings: demo.settings,
          biometrics: demo.biometrics,
          history: demo.history,
          camera: false,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('扫码配对'));
    await tester.pumpAndSettle();

    expect(find.text('输入配对码'), findsOneWidget);
    await tester.tap(find.text('输入配对码'));
    await tester.pumpAndSettle();
    expect(find.text('192.168.1.23:9876'), findsOneWidget);
    expect(find.text('ABCD-EFGH-JKLM-2345'), findsOneWidget);

    final address = find.byKey(const ValueKey('manual-pair-address'));
    final code = find.byKey(const ValueKey('manual-pair-code'));
    final continueButton = find.widgetWithText(KButton, '继续');
    final codeEditor = find.descendant(
      of: code,
      matching: find.byType(EditableText),
    );

    await tester.enterText(address, '192.168.1.23:9876');
    await tester.enterText(code, 'abcd efgh 0189');
    await tester.pump();
    expect(
      tester.widget<EditableText>(codeEditor).controller.text,
      'ABCD-EFGH',
    );
    expect(tester.widget<KButton>(continueButton).onTap, isNull);
    expect(pairing.inputs, isEmpty);

    await tester.enterText(address, '192.168.1.23:70000');
    await tester.enterText(code, 'abcd efgh-jklm 2345');
    await tester.pump();
    expect(
      tester.widget<EditableText>(codeEditor).controller.text,
      'ABCD-EFGH-JKLM-2345',
    );
    expect(tester.widget<KButton>(continueButton).onTap, isNull);

    await tester.enterText(address, '[fd00::23]:9876');
    await tester.pump();
    await tester.tap(continueButton);
    await tester.pumpAndSettle();
    final raw = pairing.inputs.single;
    final input = Uri.parse(raw);
    expect(input.scheme, 'kkdesk');
    expect(input.host, 'manual');
    expect(input.queryParameters, {
      'lan': '[fd00::23]:9876',
      'c': 'ABCD-EFGH-JKLM-2345',
    });
    final target = PairTarget.parse(raw);
    expect(target.manual, isTrue);
    expect(target.lan, ['[fd00::23]:9876']);
    expect(target.code, 'ABCDEFGHJKLM2345');
    expect(target.server, isNull);
    await tester.pump(const Duration(seconds: 5));
  });
}

class _RecordingPairingService implements PairingService {
  final inputs = <String>[];

  @override
  Future<PairRequest> begin(String input, {String? server}) async {
    inputs.add(input);
    throw const FormatException('测试配对已结束');
  }
}
