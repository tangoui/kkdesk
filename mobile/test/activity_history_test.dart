import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/activity_history.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'connections retain device ID and operating system across reloads',
    () async {
      SharedPreferences.setMockInitialValues({
        'connections': [
          jsonEncode(['旧电脑', '2026-11-25T19:00:00Z', 54000, '直连']),
        ],
      });
      final preferences = await SharedPreferences.getInstance();
      final history = ActivityHistory(preferences);
      expect(history.connections.single.os, ComputerOS.unknown);
      expect(history.connections.single.deviceId, isNull);
      await history.recordConnection((
        computer: '家庭电脑',
        deviceId: 'linux:01',
        os: ComputerOS.linux,
        start: DateTime.utc(2026, 11, 25, 19),
        duration: const Duration(minutes: 2),
        path: '中继',
      ));

      final reloaded = ActivityHistory(preferences);
      expect(reloaded.connections.first.os, ComputerOS.linux);
      expect(reloaded.connections.first.deviceId, 'linux:01');
      expect(reloaded.connections.last.computer, '旧电脑');
    },
  );

  test('new and missing system identifiers use the generic fallback', () {
    final saved = {'id': 'test', 'name': '电脑', 'path': 'direct'};
    for (final system in ComputerOS.values) {
      expect(Computer.fromJson({...saved, 'os': system.name}).os, system);
    }
    expect(Computer.fromJson(saved).os, ComputerOS.unknown);
    expect(
      Computer.fromJson({...saved, 'os': 'future-os'}).os,
      ComputerOS.unknown,
    );
  });
}
