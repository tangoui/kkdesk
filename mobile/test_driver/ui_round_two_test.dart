import 'dart:io';

import 'package:flutter_driver/flutter_driver.dart';

Future<void> main() async {
  final device = Platform.environment['KKDESK_SHOT_DEVICE']!;
  final platform = Platform.environment['KKDESK_SHOT_PLATFORM']!;
  final ios = platform == 'ios';
  final driver = await FlutterDriver.connect();
  const timeout = Duration(seconds: 15);
  String appearance = 'light';

  Future<void> tap(SerializableFinder finder) async {
    await driver.waitForTappable(finder, timeout: timeout);
    await driver.tap(finder, timeout: timeout);
  }

  Future<void> text(String label) => tap(find.text(label));
  Future<void> key(String label) => tap(find.byValueKey(label));
  Future<void> tooltip(String label) => tap(find.byTooltip(label));
  Future<void> shot(String name) async {
    await Future<void>.delayed(const Duration(milliseconds: 350));
    final directory = Directory(
      '../docs/mobile-ui-shots/round-2/$platform/$appearance',
    )..createSync(recursive: true);
    final path = '${directory.absolute.path}/$name.png';
    final command = ios
        ? 'xcrun'
        : '${Platform.environment['ANDROID_HOME']}/platform-tools/adb';
    final result = await Process.run(
      command,
      ios
          ? ['simctl', 'io', device, 'screenshot', path]
          : ['-s', device, 'exec-out', 'screencap', '-p'],
      stdoutEncoding: ios ? systemEncoding : null,
    );
    if (result.exitCode != 0) throw StateError('${result.stderr}');
    if (!ios) File(path).writeAsBytesSync(result.stdout as List<int>);
    stdout.writeln('Captured $platform/$appearance/$name');
  }

  Future<void> menu() => key('remote-ball');
  Future<void> dismissMenu() => menu();

  Future<void> pair() async {
    await key('scan');
    await text('输入配对码');
    await shot('06-pair-code');
    await tap(
      find.descendant(
        of: find.byValueKey('pair-code'),
        matching: find.byType('EditableText'),
      ),
    );
    await driver.enterText('12345678', timeout: timeout);
    await driver.waitFor(find.text('12345678'), timeout: timeout);
    await driver.sendTextInputAction(TextInputAction.done, timeout: timeout);
    await driver.runUnsynchronized(() async {
      await driver.waitFor(find.text('等待电脑确认…'), timeout: timeout);
      await shot('07-pair-verify');
    });
    await driver.waitFor(find.text('配对成功'), timeout: timeout);
    await shot('24-pair-success');
    await text('完成');
    await shot('08-top-notice');
    await tooltip('关闭提示');
  }

  Future<void> pairOutcome(String code, String status, String name) async {
    await key('scan');
    await text('输入配对码');
    await tap(
      find.descendant(
        of: find.byValueKey('pair-code'),
        matching: find.byType('EditableText'),
      ),
    );
    await driver.enterText(code, timeout: timeout);
    await driver.waitFor(find.text(code), timeout: timeout);
    await driver.sendTextInputAction(TextInputAction.done, timeout: timeout);
    await driver.waitFor(find.text(status), timeout: timeout);
    await shot(name);
    await text('取消');
  }

  Future<void> keyboard() async {
    await driver.setTextEntryEmulation(enabled: false, timeout: timeout);
    for (var attempt = 0; attempt < 2; attempt++) {
      await menu();
      await key('remote-keyboard');
      await Future<void>.delayed(const Duration(seconds: 2));
      try {
        await driver.waitForTappable(
          find.byTooltip('收起键盘'),
          timeout: const Duration(seconds: 3),
        );
        await shot('28-remote-keyboard');
        await tooltip('收起键盘');
        break;
      } on DriverError {
        if (attempt == 1) rethrow;
      }
    }
    await driver.setTextEntryEmulation(enabled: true, timeout: timeout);
  }

  Future<void> round({required bool first}) async {
    await key('devices-tab');
    await shot('01-devices');
    if (first) {
      await key('transfers-tab');
      await shot('02-transfers-empty');
      await key('devices-tab');
    }

    await tooltip('添加电脑');
    await shot('03-add');
    await text('局域网地址 + 密码');
    await shot('04-lan');
    await text('取消');
    await key('scan');
    await shot('05-scan');
    await tooltip('关闭扫码');
    await pair();
    await pairOutcome('22222222', '配对被拒绝', '25-pair-rejected');
    await pairOutcome('33333333', '配对超时', '26-pair-timeout');
    await pairOutcome('44444444', '二维码已过期', '27-pair-expired');

    await key('mine-tab');
    await shot('09-mine');
    await text('服务器设置');
    await shot('10-server');
    await text('取消');
    await text('主题');
    await shot('11-theme');
    await text('取消');
    await text('关于');
    await shot('12-about');
    await text('完成');
    await text('已配对电脑');
    await shot('13-paired-devices');
    await tap(
      find.descendant(
        of: find.byType('GroupCard'),
        matching: find.text('更改名称'),
        firstMatchOnly: true,
      ),
    );
    await shot('14-rename');
    await text('取消');
    await tap(
      find.descendant(
        of: find.byType('GroupCard'),
        matching: find.text('解除配对'),
        firstMatchOnly: true,
      ),
    );
    await shot('15-unpair');
    await text('取消');
    await tooltip('返回');
    await key('devices-tab');

    await key('connect-office');
    await shot('16-biometric');
    await text('取消');

    await key('connect-mac');
    if (!first) {
      await menu();
      await key('remote-mode');
      await text('手势说明');
    }
    await driver.waitFor(find.text('远程手势'), timeout: timeout);
    await shot('17-gestures');
    await key('gestures-done');
    await menu();
    await shot('18-remote-menu');
    await text('屏幕');
    await shot('19-screens');
    await dismissMenu();
    await menu();
    await text('画质');
    await shot('20-quality');
    await dismissMenu();
    await menu();
    await text('更多');
    await shot('21-more');
    await text('剪贴板');
    await shot('22-clipboard');
    await text('复制到手机');
    await tooltip('关闭提示');
    await keyboard();
    await menu();
    await key('remote-disconnect');
    await key('transfers-tab');
    await shot('23-transfers');
  }

  try {
    await key('mine-tab');
    await text('主题');
    await text('深色');
    appearance = 'dark';
    await key('transfers-tab');
    await shot('02-transfers-empty');
    await key('mine-tab');
    await text('主题');
    await text('浅色');
    appearance = 'light';
    await round(first: true);
    await key('mine-tab');
    await text('主题');
    await text('深色');
    appearance = 'dark';
    await round(first: false);
  } finally {
    await driver.close();
  }
}
