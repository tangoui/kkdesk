import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:local_auth/local_auth.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'activity_history.dart';
import 'computer_os.dart';
import 'lan_remote_session.dart';
import 'touch_controller.dart';

export 'computer_os.dart';

enum ConnectionPath {
  direct('直连'),
  relay('中继');

  const ConnectionPath(this.label);
  final String label;
}

class Computer {
  const Computer({
    required this.id,
    required this.name,
    required this.os,
    this.path = ConnectionPath.direct,
    this.latency,
    this.lastSeen,
    this.confirmOnHost = true,
  });

  final String id;
  final String name;
  final ComputerOS os;
  final ConnectionPath path;
  final int? latency;
  final String? lastSeen;
  final bool confirmOnHost;
  bool get online => latency != null;

  Computer renamed(String name) => Computer(
    id: id,
    name: name,
    os: os,
    path: path,
    latency: latency,
    lastSeen: lastSeen,
    confirmOnHost: confirmOnHost,
  );

  Map<String, Object?> toJson() => {
    'id': id,
    'name': name,
    'os': os.name,
    'path': path.name,
    'latency': latency,
    'lastSeen': lastSeen,
    'confirmOnHost': confirmOnHost,
  };

  factory Computer.fromJson(Map<String, dynamic> json) => Computer(
    id: json['id'] as String,
    name: json['name'] as String,
    os: ComputerOS.fromName(json['os']),
    path: ConnectionPath.values.byName(json['path'] as String),
    latency: json['latency'] as int?,
    lastSeen: json['lastSeen'] as String?,
    confirmOnHost: json['confirmOnHost'] as bool? ?? true,
  );
}

enum UnpairResult { removed, localOnly }

abstract interface class DeviceRepository implements Listenable {
  List<Computer> get computers;
  Future<void> rename(String id, String name);
  Future<UnpairResult> remove(String id);
  Future<void> add(Computer computer);
}

enum PairStatus { waiting, success, rejected, timedOut, expired }

class PairRequest {
  const PairRequest({
    required this.computer,
    required this.server,
    required this.verificationCode,
    required this.statuses,
    required this.cancel,
  });

  final Computer computer;
  final String? server;
  final String verificationCode;
  final Stream<PairStatus> statuses;
  final VoidCallback cancel;
}

abstract interface class PairingService {
  Future<PairRequest> begin(String input, {String? server});
}

enum RemoteQuality {
  auto('自动'),
  smooth('流畅'),
  high('高清');

  const RemoteQuality(this.label);
  final String label;
}

enum RemotePhase { connecting, connected, ended }

class RemoteStatus {
  const RemoteStatus({
    this.phase = RemotePhase.connecting,
    this.path = ConnectionPath.direct,
    this.latency,
    this.fps,
    this.detail,
  });

  final RemotePhase phase;
  final ConnectionPath path;
  final int? latency;
  final int? fps;
  final String? detail;
}

abstract interface class RemoteSession implements Listenable {
  RemoteStatus get status;
  double get aspectRatio;
  Widget video();
  int get screen;
  List<String> get screens;
  RemoteQuality get quality;
  bool get canChangeQuality;
  bool get canUseClipboard;
  Future<void> start();
  void pointer(PointerCommand command, int modifiers);
  void key(int hid, bool down, int modifiers);
  void setScreen(int index);
  void setQuality(RemoteQuality quality);
  Future<String> readClipboard();
  Future<void> writeClipboard(String text);
  Future<void> close();
}

abstract interface class RemoteSessions {
  RemoteSession paired(Computer computer);
  RemoteSession lan(String address, String password);
}

abstract interface class Biometrics {
  Future<bool> authenticate(String reason);
}

class DeviceBiometrics implements Biometrics {
  DeviceBiometrics() : _auth = LocalAuthentication();
  final LocalAuthentication _auth;

  @override
  Future<bool> authenticate(String reason) async {
    try {
      if (!await _auth.canCheckBiometrics) return false;
      return await _auth.authenticate(
        localizedReason: reason,
        biometricOnly: true,
        persistAcrossBackgrounding: true,
      );
    } on LocalAuthException {
      return false;
    }
  }
}

class _DemoBiometrics implements Biometrics {
  @override
  Future<bool> authenticate(String reason) async => true;
}

class MobileSettings extends ChangeNotifier {
  MobileSettings([this._prefs]);

  final SharedPreferences? _prefs;
  final _temporary = <String, String>{};

  String? _read(String key) => _temporary[key] ?? _prefs?.getString(key);

  Future<void> _write(String key, String value) async {
    _temporary[key] = value;
    notifyListeners();
    await _prefs?.setString(key, value);
  }

  ThemeMode get theme =>
      ThemeMode.values.asNameMap()[_read('theme')] ?? ThemeMode.system;
  bool get locked => _read('locked') == 'true';
  String get server => _read('server') ?? '';
  bool get hasSeenGestures => _read('gestures') == 'true';

  TouchMode touchMode(String device) =>
      TouchMode.values.asNameMap()[_read('touch:$device')] ??
      TouchMode.trackpad;

  Future<void> setTheme(ThemeMode theme) => _write('theme', theme.name);
  Future<void> setLocked(bool value) => _write('locked', value.toString());
  Future<void> setServer(String value) => _write('server', value);
  Future<void> seeGestures() => _write('gestures', 'true');
  Future<void> setTouchMode(String device, TouchMode mode) =>
      _write('touch:$device', mode.name);
}

class MobileServices {
  const MobileServices({
    required this.devices,
    required this.pairing,
    required this.sessions,
    required this.settings,
    required this.biometrics,
    required this.history,
    this.camera = true,
    this.preview = false,
  });

  factory MobileServices.demo({
    SharedPreferences? preferences,
    bool camera = false,
    bool preview = false,
    Biometrics? biometrics,
    Duration pairingDelay = const Duration(seconds: 3),
  }) => MobileServices(
    devices: DemoDeviceRepository(preferences),
    pairing: DemoPairingService(delay: pairingDelay),
    sessions: DemoRemoteSessions(),
    settings: MobileSettings(preferences),
    biometrics: biometrics ?? _DemoBiometrics(),
    history: ActivityHistory(preferences),
    camera: camera,
    preview: preview,
  );

  final DeviceRepository devices;
  final PairingService pairing;
  final RemoteSessions sessions;
  final MobileSettings settings;
  final Biometrics biometrics;
  final ActivityHistory history;
  final bool camera;
  final bool preview;
}

class DemoDeviceRepository extends ChangeNotifier implements DeviceRepository {
  DemoDeviceRepository(this._prefs) {
    final saved = _prefs?.getString('demo.devices');
    if (saved == null) return;
    try {
      _computers
        ..clear()
        ..addAll(
          (jsonDecode(saved) as List).cast<Map<String, dynamic>>().map(
            Computer.fromJson,
          ),
        );
    } on FormatException {
      _restoreSamples();
    } on ArgumentError {
      _restoreSamples();
    } on TypeError {
      _restoreSamples();
    }
  }

  static const _samples = [
    Computer(
      id: 'mac',
      name: '林舟的 MacBook Pro',
      os: ComputerOS.mac,
      latency: 18,
    ),
    Computer(
      id: 'office',
      name: '办公室台式机',
      os: ComputerOS.windows,
      path: ConnectionPath.relay,
      latency: 46,
      confirmOnHost: false,
    ),
    Computer(
      id: 'render',
      name: '渲染服务器',
      os: ComputerOS.linux,
      lastSeen: '2 小时前',
    ),
    Computer(
      id: 'home',
      name: '妈妈的电脑',
      os: ComputerOS.windows,
      lastSeen: '3 天前',
    ),
  ];
  final SharedPreferences? _prefs;
  final _computers = [..._samples];

  @override
  List<Computer> get computers => List.unmodifiable(_computers);

  void _restoreSamples() => _computers
    ..clear()
    ..addAll(_samples);

  Future<void> _save() async {
    notifyListeners();
    await _prefs?.setString('demo.devices', jsonEncode(_computers));
  }

  @override
  Future<void> rename(String id, String name) async {
    final index = _computers.indexWhere((computer) => computer.id == id);
    if (index < 0 || name.trim().isEmpty) return;
    _computers[index] = _computers[index].renamed(name.trim());
    await _save();
  }

  @override
  Future<UnpairResult> remove(String id) async {
    _computers.removeWhere((computer) => computer.id == id);
    await _save();
    return UnpairResult.removed;
  }

  @override
  Future<void> add(Computer computer) async {
    if (!_computers.any((item) => item.id == computer.id)) {
      _computers.insert(0, computer);
      await _save();
    }
  }
}

class DemoPairingService implements PairingService {
  const DemoPairingService({this.delay = const Duration(seconds: 3)});
  final Duration delay;

  @override
  Future<PairRequest> begin(String input, {String? server}) async {
    final raw = input.trim();
    final uri = Uri.tryParse(raw);
    final isCode = RegExp(r'^\d{8}$').hasMatch(raw);
    final params = uri?.queryParameters ?? const <String, String>{};
    if (!isCode &&
        (uri?.scheme != 'kkdesk' ||
            uri?.host != 'pair' ||
            params['v'] != '1' ||
            (params['id']?.isEmpty ?? true) ||
            (params['c']?.isEmpty ?? true) ||
            (params['fp']?.isEmpty ?? true))) {
      throw const FormatException('请扫描 kkdesk 的配对二维码，或输入 8 位配对码。');
    }
    final code = params['c'] ?? raw;
    final result = switch (code) {
      '22222222' => PairStatus.rejected,
      '33333333' => PairStatus.timedOut,
      '44444444' => PairStatus.expired,
      _ => PairStatus.success,
    };
    final host = params['s'] ?? server;
    final controller = StreamController<PairStatus>();
    final timer = Timer(delay, () {
      controller.add(result);
      unawaited(controller.close());
    });
    return PairRequest(
      computer: Computer(
        id: params['id'] ?? 'mac',
        name: '林舟的 MacBook Pro',
        os: ComputerOS.mac,
        latency: 18,
      ),
      server: host?.isNotEmpty == true
          ? host
          : (isCode ? 'desk.example.com' : null),
      verificationCode: '4829',
      statuses: controller.stream,
      cancel: () {
        timer.cancel();
        if (!controller.isClosed) unawaited(controller.close());
      },
    );
  }
}

class DemoRemoteSessions implements RemoteSessions {
  @override
  RemoteSession paired(Computer computer) => _DemoRemoteSession(computer);

  @override
  RemoteSession lan(String address, String password) =>
      LanRemoteSession(address, password);
}

class _DemoRemoteSession extends ChangeNotifier implements RemoteSession {
  _DemoRemoteSession(this.computer);
  final Computer computer;
  RemoteStatus _status = const RemoteStatus();
  RemoteQuality _quality = RemoteQuality.auto;
  int _screen = 0;
  String _clipboard = '来自远程电脑的演示文本';
  bool _closed = false;

  @override
  RemoteStatus get status => _status;
  @override
  double get aspectRatio => 16 / 9;
  @override
  Widget video() => Image.asset(
    'assets/demo_desktop.jpg',
    fit: BoxFit.fill,
    color: _screen == 1 ? const Color(0xff426392) : null,
    colorBlendMode: BlendMode.softLight,
  );
  @override
  int get screen => _screen;
  @override
  List<String> get screens => const ['主屏幕', '屏幕 2'];
  @override
  RemoteQuality get quality => _quality;
  @override
  bool get canChangeQuality => true;
  @override
  bool get canUseClipboard => true;

  @override
  Future<void> start() async {
    await Future<void>.delayed(const Duration(milliseconds: 250));
    if (_closed) return;
    _status = RemoteStatus(
      phase: RemotePhase.connected,
      path: computer.path,
      latency: computer.latency,
      fps: 60,
    );
    notifyListeners();
  }

  @override
  void pointer(PointerCommand command, int modifiers) {}
  @override
  void key(int hid, bool down, int modifiers) {}

  @override
  void setScreen(int index) {
    _screen = index;
    notifyListeners();
  }

  @override
  void setQuality(RemoteQuality quality) {
    _quality = quality;
    notifyListeners();
  }

  @override
  Future<String> readClipboard() async => _clipboard;

  @override
  Future<void> writeClipboard(String text) async => _clipboard = text;

  @override
  Future<void> close() async {
    _closed = true;
    dispose();
  }
}
