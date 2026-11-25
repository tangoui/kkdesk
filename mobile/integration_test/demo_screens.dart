import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_driver/driver_extension.dart';
import 'package:kkdesk_mobile/main.dart';
import 'package:kkdesk_mobile/mobile_services.dart';

Future<void> main() async {
  enableFlutterDriverExtension();
  await SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  runApp(
    KkdeskApp(
      services: MobileServices.demo(
        preview: true,
        pairingDelay: const Duration(seconds: 4),
      ),
    ),
  );
}
