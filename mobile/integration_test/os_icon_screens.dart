import 'package:flutter/material.dart';
import 'package:flutter_driver/driver_extension.dart';
import 'package:kkdesk_mobile/main.dart';
import 'package:kkdesk_mobile/mobile_services.dart';

Future<void> main() async {
  enableFlutterDriverExtension();
  final services = MobileServices.demo();
  final now = DateTime.now();
  final computers = services.devices.computers.take(3).toList();
  for (var index = 0; index < computers.length; index++) {
    final computer = computers[index];
    await services.history.recordConnection((
      computer: computer.name,
      deviceId: computer.id,
      os: computer.os,
      start: now.subtract(Duration(hours: index + 1)),
      duration: Duration(minutes: index + 1, seconds: 18),
      path: computer.path.label,
    ));
  }
  services.history.recordClipboard((
    computer: computers.first.name,
    deviceId: computers.first.id,
    os: computers.first.os,
    time: now.subtract(const Duration(hours: 1)),
    text: '来自远程电脑的演示文本',
  ));
  await services.settings.setTheme(ThemeMode.light);
  runApp(KkdeskApp(services: services));
}
