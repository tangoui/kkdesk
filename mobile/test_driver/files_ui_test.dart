import 'dart:io';

import 'package:flutter_driver/flutter_driver.dart';

Future<void> main() async {
  final env = Platform.environment;
  final platform = env['KKDESK_FILES_PLATFORM']!;
  final device = env['KKDESK_FILES_DEVICE']!;
  final host = env['KKDESK_FILES_ADDRESS']!;
  final password = env['KKDESK_FILES_PASSWORD']!;
  final fixture = env['KKDESK_FILES_FIXTURE']!;
  final output = Directory('../docs/mobile-ui-shots/files/$platform')
    ..createSync(recursive: true);
  final driver = await FlutterDriver.connect();
  await Future<void>.delayed(const Duration(seconds: 1));
  const timeout = Duration(seconds: 20);
  var appearance = 'light';

  Future<void> tap(SerializableFinder finder) async {
    await driver.waitForTappable(finder, timeout: timeout);
    await driver.tap(finder, timeout: timeout);
  }

  Future<void> text(String label) => tap(find.text(label));
  Future<void> tip(String label) => tap(find.byTooltip(label));
  Future<void> key(String label) => tap(find.byValueKey(label));
  Future<void> shot(String name) async {
    final path = '${output.absolute.path}/$appearance-$name.png';
    final ios = platform == 'ios';
    final result = await Process.run(
      ios ? 'xcrun' : '${env['ANDROID_HOME']}/platform-tools/adb',
      ios
          ? ['simctl', 'io', device, 'screenshot', path]
          : ['-s', device, 'exec-out', 'screencap', '-p'],
      stdoutEncoding: ios ? systemEncoding : null,
    );
    if (result.exitCode != 0) throw StateError('${result.stderr}');
    if (!ios) File(path).writeAsBytesSync(result.stdout as List<int>);
    stdout.writeln('Captured $path');
  }

  Future<void> connect() async {
    await key('transfers-tab');
    await text('文件传输');
    await tap(
      find.descendant(
        of: find.byValueKey('lan-address'),
        matching: find.byType('EditableText'),
      ),
    );
    await driver.enterText(host, timeout: timeout);
    await driver.waitFor(find.text(host), timeout: timeout);
    await tap(
      find.descendant(
        of: find.byValueKey('lan-password'),
        matching: find.byType('EditableText'),
      ),
    );
    await driver.enterText(password, timeout: timeout);
    await driver.sendTextInputAction(TextInputAction.done, timeout: timeout);
    await driver.waitFor(find.text('电脑文件'), timeout: timeout);
    await driver.waitForTappable(find.byTooltip('位置'), timeout: timeout);
  }

  Future<void> target(String label) async {
    await tip('位置');
    await text('前往路径');
    await tap(find.byType('EditableText'));
    await driver.enterText('$fixture/$label', timeout: timeout);
    await driver.waitFor(find.text('$fixture/$label'), timeout: timeout);
    await driver.sendTextInputAction(TextInputAction.done, timeout: timeout);
    await driver.waitForTappable(find.text(label), timeout: timeout);
  }

  Future<void> uploadFromPhotos() async {
    await text('照片和视频');
    stdout.writeln('NATIVE_PICKER_READY $platform');
    final signal = File('$fixture/$platform-photo-selected');
    for (var i = 0; i < 240 && !signal.existsSync(); i++) {
      await Future<void>.delayed(const Duration(milliseconds: 250));
    }
    if (!signal.existsSync()) {
      throw StateError('Native photo selection timed out');
    }
    await driver.waitFor(find.text('传输队列'), timeout: timeout);
    await driver.waitFor(find.text('文件传输记录'), timeout: timeout);
    await driver.waitFor(find.text('暂无进行中的传输'), timeout: timeout);
  }

  Future<void> phase({required bool photos}) async {
    final failureName = '已移除-$platform-$appearance.txt';
    final failed = File('$fixture/source/$failureName')
      ..writeAsStringSync('removed');
    await connect();
    await target('source');
    await driver.waitFor(find.text(failureName), timeout: timeout);
    await shot('browser');
    await text('多选');
    await text('进度样本.bin');
    await text('下载到手机（1）');
    await driver.runUnsynchronized(() async {
      await driver.waitFor(find.text('传输队列'), timeout: timeout);
      await driver.waitFor(find.text('取消'), timeout: timeout);
      await Future<void>.delayed(const Duration(milliseconds: 400));
      await shot('running');
    });
    await driver.waitFor(
      find.text('暂无进行中的传输'),
      timeout: const Duration(seconds: 45),
    );
    await tip('返回');
    await text('多选');
    await text('示例文件夹');
    await text('下载到手机（1）');
    await driver.waitFor(find.text('示例文件夹'), timeout: timeout);
    await driver.waitFor(find.text('暂无进行中的传输'), timeout: timeout);
    await shot('completed');
    await tip('返回');
    failed.deleteSync();
    await text(failureName);
    await text('下载到手机（1）');
    await driver.waitFor(find.text('找不到文件或文件夹'), timeout: timeout);
    await shot('failed');
    await tip('返回');
    await target('received-$platform');
    await text('上传');
    await driver.waitForTappable(find.text('照片和视频'), timeout: timeout);
    await Future<void>.delayed(const Duration(milliseconds: 350));
    await shot('upload');
    if (photos) {
      await uploadFromPhotos();
      await tip('返回');
    } else {
      await text('取消');
    }
    await tip('返回');
    await driver.waitFor(find.text('文件传输'), timeout: timeout);
    await shot('transfers');
  }

  try {
    if (env['KKDESK_FILES_PHOTO'] == 'skip') {
      appearance = env['KKDESK_FILES_THEME'] ?? 'light';
      if (env['KKDESK_FILES_RETURN_HOME'] == '1') {
        await tip('返回');
        await tip('返回');
      }
      await key('mine-tab');
      await text('主题');
      await text(appearance == 'dark' ? '深色' : '浅色');
      await phase(photos: false);
      return;
    }
    if (env['KKDESK_FILES_RESUME_PHOTO'] == '1') {
      await driver.waitFor(find.text('暂无进行中的传输'), timeout: timeout);
      await tip('返回');
      await tip('返回');
      await driver.waitFor(find.text('文件传输'), timeout: timeout);
      await shot('transfers');
    } else {
      await key('mine-tab');
      await text('主题');
      await text('浅色');
      await phase(photos: true);
    }
    await key('mine-tab');
    await text('主题');
    await text('深色');
    appearance = 'dark';
    await phase(photos: false);
  } finally {
    await driver.close();
  }
}
