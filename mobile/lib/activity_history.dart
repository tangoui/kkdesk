import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'computer_os.dart';

typedef ConnectionEntry = ({
  String computer,
  String? deviceId,
  ComputerOS os,
  DateTime start,
  Duration duration,
  String path,
});
typedef ClipboardEntry = ({
  String computer,
  String deviceId,
  ComputerOS os,
  DateTime time,
  String text,
});

class ActivityHistory extends ChangeNotifier {
  ActivityHistory([this._preferences]) {
    for (final saved
        in _preferences?.getStringList('connections') ?? const <String>[]) {
      try {
        final data = jsonDecode(saved) as List<dynamic>;
        if (data.length < 4) continue;
        _connections.add((
          computer: data[0] as String,
          start: DateTime.parse(data[1] as String),
          duration: Duration(milliseconds: data[2] as int),
          path: data[3] as String,
          os: data.length > 4
              ? ComputerOS.fromName(data[4])
              : ComputerOS.unknown,
          deviceId: data.length > 5 && data[5] is String
              ? data[5] as String
              : null,
        ));
      } on FormatException {
        continue;
      } on TypeError {
        continue;
      }
    }
  }
  final SharedPreferences? _preferences;
  final _connections = <ConnectionEntry>[];
  final _clipboard = <ClipboardEntry>[];

  List<ConnectionEntry> get connections => List.unmodifiable(_connections);
  List<ClipboardEntry> get clipboard => List.unmodifiable(_clipboard);

  Future<void> recordConnection(ConnectionEntry entry) async {
    _connections.insert(0, entry);
    if (_connections.length > 30) _connections.removeLast();
    notifyListeners();
    await _preferences?.setStringList('connections', [
      for (final item in _connections)
        jsonEncode([
          item.computer,
          item.start.toIso8601String(),
          item.duration.inMilliseconds,
          item.path,
          item.os.name,
          item.deviceId,
        ]),
    ]);
  }

  void recordClipboard(ClipboardEntry entry) {
    if (entry.text.isEmpty) return;
    _clipboard.insert(0, entry);
    if (_clipboard.length > 20) _clipboard.removeLast();
    notifyListeners();
  }
}
