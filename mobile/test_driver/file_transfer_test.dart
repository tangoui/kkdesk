import 'dart:convert';
import 'dart:io';

import 'package:flutter_driver/flutter_driver.dart';

Future<void> main() async {
  final driver = await FlutterDriver.connect();
  try {
    final report = await driver.requestData(
      'verify',
      timeout: const Duration(minutes: 8),
    );
    final data = jsonDecode(report) as Map;
    stdout.writeln(const JsonEncoder.withIndent('  ').convert(data));
    if (data['error'] != null) exitCode = 1;
  } finally {
    await driver.close();
  }
}
