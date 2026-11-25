import 'dart:io';

import 'package:flutter_driver/flutter_driver.dart';

Future<void> main() async {
  final device = Platform.environment['KKDESK_SHOT_DEVICE']!;
  final platform = Platform.environment['KKDESK_SHOT_PLATFORM']!;
  final output = Directory('../docs/mobile-ui-shots/$platform')
    ..createSync(recursive: true);
  final driver = await FlutterDriver.connect();
  const timeout = Duration(seconds: 15);

  Future<void> tap(SerializableFinder finder) =>
      driver.tap(finder, timeout: timeout);

  Future<void> screenshot(String name) async {
    await Future<void>.delayed(const Duration(milliseconds: 500));
    final path = '${output.absolute.path}/$name.png';
    final ios = platform == 'ios';
    final command = ios
        ? 'xcrun'
        : '${Platform.environment['ANDROID_HOME']}/platform-tools/adb';
    final arguments = ios
        ? ['simctl', 'io', device, 'screenshot', path]
        : ['-s', device, 'exec-out', 'screencap', '-p'];
    final result = await Process.run(
      command,
      arguments,
      stdoutEncoding: ios ? systemEncoding : null,
    );
    if (result.exitCode != 0) throw StateError('${result.stderr}');
    if (!ios) File(path).writeAsBytesSync(result.stdout as List<int>);
    stdout.writeln('Captured $platform/$name.png');
  }

  Future<void> code(
    String input,
    String status,
    String shot, {
    bool waiting = false,
  }) async {
    await tap(find.byValueKey('scan'));
    await tap(find.text('输入配对码'));
    await tap(find.byValueKey('pair-code'));
    await driver.enterText(input, timeout: timeout);
    await driver.waitFor(find.text(input), timeout: timeout);
    await driver.sendTextInputAction(TextInputAction.done, timeout: timeout);
    if (waiting) {
      await driver.runUnsynchronized(() async {
        await driver.waitFor(find.text('等待电脑确认…'), timeout: timeout);
        await screenshot('03-pair-waiting');
      });
    }
    await driver.waitFor(find.text(status), timeout: timeout);
    await screenshot(shot);
  }

  try {
    await driver.waitFor(find.text('在线 · 2'), timeout: timeout);
    await screenshot('01-devices-light');
    await tap(find.byValueKey('scan'));
    await driver.waitFor(find.text('输入配对码'), timeout: timeout);
    await screenshot('02-scan');
    await driver.tap(find.byTooltip('关闭扫码'), timeout: timeout);

    await code('12345678', '配对成功', '04-pair-success', waiting: true);
    await tap(find.text('完成'));
    await driver.waitFor(find.text('已配对'), timeout: timeout);
    await screenshot('05-paired-banner');
    await driver.tap(find.byTooltip('关闭提示'), timeout: timeout);

    await code('22222222', '配对被拒绝', '06-pair-rejected');
    await tap(find.text('取消'));
    await code('33333333', '配对超时', '07-pair-timeout');
    await tap(find.text('取消'));
    await code('44444444', '二维码已过期', '08-pair-expired');
    await tap(find.text('取消'));

    await tap(find.byValueKey('connect-mac'));
    await tap(find.byValueKey('gestures-done'));
    await driver.waitFor(find.byValueKey('trackpad-cursor'), timeout: timeout);
    await screenshot('09-remote-trackpad');
    await tap(find.byValueKey('remote-ball'));
    await driver.waitFor(find.byValueKey('remote-mode'), timeout: timeout);
    await screenshot('10-remote-menu');
    await tap(find.byValueKey('remote-mode'));
    await tap(find.byValueKey('mode-direct'));
    await driver.waitFor(find.text('已切换到直接点按模式'), timeout: timeout);
    await screenshot('11-remote-direct');
    await driver.setTextEntryEmulation(enabled: false, timeout: timeout);
    for (var attempt = 0; attempt < 2; attempt++) {
      await tap(find.byValueKey('remote-ball'));
      await tap(find.byValueKey('remote-keyboard'));
      // The shortcut bar moves while the system keyboard animates in.
      await Future<void>.delayed(const Duration(seconds: 2));
      try {
        await driver.waitForTappable(
          find.text('Ctrl'),
          timeout: const Duration(seconds: 3),
        );
        await tap(find.text('Ctrl'));
        break;
      } on DriverError {
        if (attempt == 1) rethrow;
      }
    }
    await screenshot('12-remote-keyboard');
    await tap(find.byTooltip('收起键盘'));
    await tap(find.byValueKey('remote-ball'));
    await tap(find.byValueKey('remote-disconnect'));

    await tap(find.byValueKey('mine-tab'));
    await driver.waitFor(find.text('服务器设置'), timeout: timeout);
    await screenshot('13-mine');
    await tap(find.text('主题'));
    await tap(find.text('深色'));
    await driver.tap(find.byValueKey('devices-tab'), timeout: timeout);
    await screenshot('14-devices-dark');
  } finally {
    await driver.close();
  }
}
