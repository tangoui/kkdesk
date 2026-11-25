import 'dart:io';

import 'package:flutter_driver/flutter_driver.dart';

Future<void> main() async {
  final device = Platform.environment['KKDESK_SHOT_DEVICE']!;
  final driver = await FlutterDriver.connect();
  final output = Directory('../docs/mobile-ui-shots/brand/system-icons')
    ..createSync(recursive: true);
  const timeout = Duration(seconds: 15);

  Future<void> tap(SerializableFinder finder) =>
      driver.tap(finder, timeout: timeout);

  Future<void> capture(String name) async {
    await Future<void>.delayed(const Duration(milliseconds: 500));
    final result = await Process.run('xcrun', [
      'simctl',
      'io',
      device,
      'screenshot',
      '${output.absolute.path}/$name.png',
    ]);
    if (result.exitCode != 0) throw StateError('${result.stderr}');
    stdout.writeln('Captured $name.png');
  }

  try {
    for (final appearance in ['light', 'dark']) {
      await tap(find.byValueKey('devices-tab'));
      await driver.waitFor(find.text('渲染服务器'), timeout: timeout);
      await capture('devices-$appearance');
      await tap(find.byValueKey('transfers-tab'));
      await driver.waitFor(find.text('最近连接'), timeout: timeout);
      await capture('transfers-$appearance');
      if (appearance == 'light') {
        await tap(find.byValueKey('mine-tab'));
        await tap(find.text('主题'));
        await tap(find.text('深色'));
      }
    }
  } finally {
    await driver.close();
  }
}
