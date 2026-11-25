import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/ui/kit/kit.dart';

void main() {
  test('app source uses kkdesk controls', () {
    final forbidden = RegExp(
      r'\b(AlertDialog|SnackBar|ListTile|AppBar|CircularProgressIndicator|FilledButton|TextButton|OutlinedButton|TextField|TextFormField|showDialog|showModalBottomSheet|showAboutDialog)\b',
    );
    final violations = Directory('lib')
        .listSync(recursive: true)
        .whereType<File>()
        .where((file) => file.path.endsWith('.dart'))
        .expand(
          (file) => forbidden
              .allMatches(file.readAsStringSync())
              .map((match) => '${file.path}: ${match.group(0)}'),
        );
    expect(violations, isEmpty);
  });

  testWidgets('input clears text and dialogs return their selected action', (
    tester,
  ) async {
    final controller = TextEditingController(text: '示例');
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: Column(
              children: [
                KInput(controller: controller),
                KButton(
                  label: '打开',
                  onTap: () async {
                    if (await kConfirm(
                              context,
                              title: '移除？',
                              confirm: '移除',
                              danger: true,
                            ) ==
                            true &&
                        context.mounted) {
                      KNotice.show(context, '已移除', kind: KNoticeKind.success);
                    }
                  },
                ),
              ],
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.byTooltip('清除'));
    await tester.pump();
    expect(controller.text, isEmpty);
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    expect(find.byType(KDialog), findsOneWidget);
    await tester.tap(find.text('移除'));
    await tester.pumpAndSettle();
    expect(find.text('已移除'), findsOneWidget);
    await tester.pump(const Duration(seconds: 5));
  });
}
