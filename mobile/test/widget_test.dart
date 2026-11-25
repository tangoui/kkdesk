import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/main.dart';

void main() {
  testWidgets('shows the LAN sign-in form', (tester) async {
    await tester.pumpWidget(const KkdeskApp());

    expect(find.text('电脑地址'), findsOneWidget);
    expect(find.text('连接密码'), findsOneWidget);
    expect(find.text('连接'), findsOneWidget);
  });
}
