import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'mobile_services.dart';
import 'ui/common.dart';
import 'ui/devices.dart';
import 'ui/pairing.dart';
import 'ui/remote.dart';
import 'ui/settings.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  final preferences = await SharedPreferences.getInstance();
  runApp(
    KkdeskApp(
      services: MobileServices.demo(
        preferences: preferences,
        camera: true,
        biometrics: DeviceBiometrics(),
      ),
    ),
  );
}

class KkdeskApp extends StatefulWidget {
  const KkdeskApp({super.key, required this.services});
  final MobileServices services;

  @override
  State<KkdeskApp> createState() => _KkdeskAppState();
}

class _KkdeskAppState extends State<KkdeskApp> with WidgetsBindingObserver {
  late bool _covered = widget.services.settings.locked;
  bool _authenticating = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if ((state == AppLifecycleState.hidden ||
            state == AppLifecycleState.paused) &&
        !_authenticating &&
        widget.services.settings.locked &&
        !_covered) {
      setState(() => _covered = true);
    }
  }

  Future<bool> _auth(String reason) async {
    if (_authenticating) return false;
    _authenticating = true;
    try {
      return await widget.services.biometrics.authenticate(reason);
    } finally {
      _authenticating = false;
    }
  }

  ThemeData _theme(Brightness brightness) => ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorSchemeSeed: blue,
    scaffoldBackgroundColor: brightness == Brightness.dark
        ? Colors.black
        : const Color(0xfff2f2f7),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: const Color(0xff0865e0),
        foregroundColor: Colors.white,
        minimumSize: const Size(0, 49),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(13)),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.services.settings,
    builder: (context, _) => MaterialApp(
      title: 'kkdesk',
      debugShowCheckedModeBanner: false,
      theme: _theme(Brightness.light),
      darkTheme: _theme(Brightness.dark),
      themeMode: widget.services.settings.theme,
      locale: const Locale('zh', 'CN'),
      supportedLocales: const [Locale('zh', 'CN')],
      localizationsDelegates: GlobalMaterialLocalizations.delegates,
      home: _Home(services: widget.services, auth: _auth),
      builder: (context, child) => Stack(
        fit: StackFit.expand,
        children: [
          ExcludeSemantics(
            excluding: _covered,
            child: IgnorePointer(ignoring: _covered, child: child),
          ),
          if (_covered)
            _AppLock(
              onUnlock: () async {
                final unlocked = await _auth('解锁 kkdesk');
                if (unlocked && mounted) setState(() => _covered = false);
                return unlocked;
              },
            ),
        ],
      ),
    ),
  );
}

class _AppLock extends StatefulWidget {
  const _AppLock({required this.onUnlock});
  final Future<bool> Function() onUnlock;

  @override
  State<_AppLock> createState() => _AppLockState();
}

class _AppLockState extends State<_AppLock> {
  bool _busy = false;
  bool _failed = false;

  Future<void> _unlock() async {
    setState(() => _busy = true);
    final ok = await widget.onUnlock();
    if (mounted) {
      setState(() {
        _busy = false;
        _failed = !ok;
      });
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: context.background,
    body: SafeArea(
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(CupertinoIcons.lock_shield, color: context.action, size: 76),
            const SizedBox(height: 16),
            const PageTitle('kkdesk 已锁定'),
            const SizedBox(height: 10),
            Text(
              _failed ? '未通过验证，请重试' : '请使用面容 ID 或指纹解锁',
              style: TextStyle(color: context.muted),
            ),
            const SizedBox(height: 24),
            FilledButton(
              onPressed: _busy ? null : _unlock,
              child: const Text('解锁'),
            ),
          ],
        ),
      ),
    ),
  );
}

class _Home extends StatefulWidget {
  const _Home({required this.services, required this.auth});
  final MobileServices services;
  final Future<bool> Function(String reason) auth;

  @override
  State<_Home> createState() => _HomeState();
}

class _HomeState extends State<_Home> {
  int _tab = 0;
  bool _connecting = false;
  Computer? _paired;
  Timer? _toast;
  MobileServices get _services => widget.services;

  @override
  void initState() {
    super.initState();
    if (const bool.fromEnvironment('KKDESK_SPIKE_METRICS')) {
      const address = String.fromEnvironment('KKDESK_SPIKE_ADDRESS');
      const password = String.fromEnvironment('KKDESK_SPIKE_PASSWORD');
      if (address.isNotEmpty && password.isNotEmpty) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _launchLan(address, password);
        });
      }
    }
  }

  @override
  void dispose() {
    _toast?.cancel();
    super.dispose();
  }

  Future<bool> _authorize(String reason) async {
    final authorized = await widget.auth(reason);
    if (!authorized && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('生物识别未通过。请重试，或在系统设置中录入面容或指纹。')),
      );
    }
    return authorized;
  }

  Future<void> _scan() async {
    FocusScope.of(context).unfocus();
    final input = await Navigator.push<String>(
      context,
      MaterialPageRoute(
        builder: (_) =>
            ScanPage(camera: _services.camera, preview: _services.preview),
      ),
    );
    if (input == null || !mounted) return;
    try {
      final request = await _services.pairing.begin(
        input,
        server: _services.settings.server,
      );
      if (!mounted) {
        request.cancel();
        return;
      }
      PairStatus? result;
      try {
        result = await appSheet<PairStatus>(
          context,
          (_) => PairVerification(request: request),
        );
      } finally {
        request.cancel();
      }
      if (!mounted) return;
      if (result == PairStatus.success) {
        await _services.devices.add(request.computer);
        if (!mounted) return;
        _toast?.cancel();
        setState(() {
          _tab = 0;
          _paired = request.computer;
        });
        _toast = Timer(const Duration(seconds: 4), () {
          if (mounted) setState(() => _paired = null);
        });
      } else if (result != null) {
        await _scan();
      }
    } on FormatException catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(error.message)));
      }
    }
  }

  Future<void> _add() async {
    FocusScope.of(context).unfocus();
    final action = await appSheet<bool>(
      context,
      (context) => Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('添加电脑', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 10),
          ListTile(
            leading: Icon(CupertinoIcons.viewfinder, color: context.action),
            title: const Text('扫码配对'),
            trailing: const Icon(CupertinoIcons.chevron_right, size: 17),
            onTap: () => Navigator.pop(context, true),
          ),
          ListTile(
            leading: Icon(CupertinoIcons.link, color: context.action),
            title: const Text('局域网地址 + 密码'),
            trailing: const Icon(CupertinoIcons.chevron_right, size: 17),
            onTap: () => Navigator.pop(context, false),
          ),
        ],
      ),
    );
    if (!mounted || action == null) return;
    if (action) {
      await _scan();
    } else {
      final target = await appSheet<({String address, String password})>(
        context,
        (_) => const LanSignIn(),
      );
      if (target != null && mounted) {
        await _launchLan(target.address, target.password);
      }
    }
  }

  Future<void> _launchLan(String address, String password) async {
    if (_services.settings.locked && !await _authorize('验证身份以开始局域网连接')) return;
    if (!mounted) return;
    await _showRemote(
      Computer(id: 'lan:$address', name: address, os: ComputerOS.unknown),
      _services.sessions.lan(address, password),
      viewOnly: false,
    );
  }

  Future<void> _connect(Computer computer, {bool viewOnly = false}) async {
    if (_connecting) return;
    _connecting = true;
    FocusScope.of(context).unfocus();
    try {
      if (!computer.confirmOnHost || _services.settings.locked) {
        if (!await _authorize('验证身份以连接「${computer.name}」')) return;
      }
      if (!mounted) return;
      await _showRemote(
        computer,
        _services.sessions.paired(computer),
        viewOnly: viewOnly,
      );
    } finally {
      _connecting = false;
    }
  }

  Future<void> _showRemote(
    Computer computer,
    RemoteSession session, {
    required bool viewOnly,
  }) => Navigator.push<void>(
    context,
    MaterialPageRoute(
      builder: (_) => RemotePage(
        computer: computer,
        session: session,
        settings: _services.settings,
        viewOnly: viewOnly,
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => AnnotatedRegion<SystemUiOverlayStyle>(
    value:
        (context.isDark
                ? SystemUiOverlayStyle.light
                : SystemUiOverlayStyle.dark)
            .copyWith(
              systemNavigationBarColor: Colors.transparent,
              systemNavigationBarIconBrightness: context.isDark
                  ? Brightness.light
                  : Brightness.dark,
            ),
    child: Scaffold(
      backgroundColor: context.background,
      body: SafeArea(
        bottom: false,
        child: Stack(
          children: [
            IndexedStack(
              index: _tab,
              children: [
                DevicesPage(
                  devices: _services.devices,
                  scan: _scan,
                  add: _add,
                  connect: _connect,
                ),
                SettingsPage(services: _services, authenticate: _authorize),
              ],
            ),
            if (_tab == 0 && _paired != null)
              Positioned(
                top: 5,
                left: 21,
                right: 21,
                child: _PairedBanner(
                  computer: _paired!,
                  dismiss: () => setState(() => _paired = null),
                ),
              ),
          ],
        ),
      ),
      bottomNavigationBar: DecoratedBox(
        decoration: BoxDecoration(
          color: context.card,
          border: Border(top: BorderSide(color: context.line)),
        ),
        child: SafeArea(
          top: false,
          child: Row(
            children: [
              _tabItem(0, '设备', CupertinoIcons.device_desktop),
              _tabItem(1, '我的', CupertinoIcons.person),
            ],
          ),
        ),
      ),
    ),
  );

  Widget _tabItem(int index, String title, IconData icon) {
    final color = _tab == index ? context.action : context.muted;
    return Expanded(
      child: Semantics(
        key: ValueKey(index == 0 ? 'devices-tab' : 'mine-tab'),
        selected: _tab == index,
        button: true,
        child: InkWell(
          onTap: () {
            FocusScope.of(context).unfocus();
            setState(() => _tab = index);
          },
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 7),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(icon, color: color, size: 25),
                const SizedBox(height: 4),
                Text(
                  title,
                  style: TextStyle(
                    color: color,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _PairedBanner extends StatelessWidget {
  const _PairedBanner({required this.computer, required this.dismiss});
  final Computer computer;
  final VoidCallback dismiss;

  @override
  Widget build(BuildContext context) => Material(
    elevation: 9,
    shadowColor: Colors.black26,
    color: context.card,
    borderRadius: BorderRadius.circular(17),
    child: ListTile(
      leading: const CircleAvatar(
        radius: 17,
        backgroundColor: green,
        child: Icon(CupertinoIcons.check_mark, color: Colors.white, size: 19),
      ),
      title: const Text('已配对', style: TextStyle(fontWeight: FontWeight.w700)),
      subtitle: Text(
        '${computer.name} 已加入设备列表',
        style: TextStyle(color: context.muted, fontSize: 12),
      ),
      trailing: IconButton(
        tooltip: '关闭提示',
        onPressed: dismiss,
        icon: Icon(CupertinoIcons.xmark, size: 16, color: context.muted),
      ),
    ),
  );
}
