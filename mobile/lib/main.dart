import 'dart:ui' show ImageFilter;

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart' show kDebugMode;
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'mobile_services.dart';
import 'pairing/services.dart';
import 'pairing/transport.dart';
import 'ui/common.dart';
import 'ui/devices.dart';
import 'ui/files/file_browser.dart';
import 'ui/files/transfer_center.dart';
import 'lan_remote_session.dart';
import 'ui/pairing.dart';
import 'ui/remote.dart';
import 'ui/settings.dart';
import 'ui/transfers.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  final preferences = await SharedPreferences.getInstance();
  const demo = kDebugMode && bool.fromEnvironment('KKDESK_DEMO');
  runApp(
    KkdeskApp(
      filePreferences: SharedPreferencesAsync(),
      services: demo
          ? MobileServices.demo(preferences: preferences, camera: true)
          : await realMobileServices(preferences),
    ),
  );
}

class KkdeskApp extends StatefulWidget {
  const KkdeskApp({super.key, required this.services, this.filePreferences});
  final MobileServices services;
  final SharedPreferencesAsync? filePreferences;

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
    splashFactory: NoSplash.splashFactory,
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
      home: _Home(
        services: widget.services,
        auth: _auth,
        filePreferences: widget.filePreferences,
      ),
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
            const BrandMark(size: 72),
            const SizedBox(height: 16),
            const PageTitle('kkdesk 已锁定'),
            const SizedBox(height: 10),
            Text(
              _failed ? '未通过验证，请重试' : '请使用面容 ID 或指纹解锁',
              style: TextStyle(color: context.muted),
            ),
            const SizedBox(height: 24),
            SizedBox(
              width: 180,
              child: KButton(label: '解锁', onTap: _busy ? null : _unlock),
            ),
          ],
        ),
      ),
    ),
  );
}

class _Home extends StatefulWidget {
  const _Home({
    required this.services,
    required this.auth,
    this.filePreferences,
  });
  final MobileServices services;
  final Future<bool> Function(String reason) auth;
  final SharedPreferencesAsync? filePreferences;

  @override
  State<_Home> createState() => _HomeState();
}

class _HomeState extends State<_Home> {
  int _tab = 0;
  bool _connecting = false;
  late final _transfers = TransferCenter(preferences: widget.filePreferences);
  MobileServices get _services => widget.services;

  @override
  void initState() {
    super.initState();
    if (_services.sessions case final PairedRemoteSessions sessions) {
      sessions.notices.addListener(_biometricNotice);
    }
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

  void _biometricNotice() {
    final sessions = _services.sessions;
    if (sessions is! PairedRemoteSessions || !mounted) return;
    final message = sessions.notices.value;
    if (message != null) {
      sessions.notices.value = null;
      KNotice.show(context, message);
    }
  }

  @override
  void dispose() {
    if (_services.sessions case final PairedRemoteSessions sessions) {
      sessions.notices.removeListener(_biometricNotice);
    }
    _transfers.dispose();
    super.dispose();
  }

  Future<void> _connectFiles([Computer? computer]) async {
    if (_connecting) return;
    _connecting = true;
    try {
      FocusScope.of(context).unfocus();
      if (computer != null && _services.sessions is PairedRemoteSessions) {
        if (_services.settings.locked && !await _authorize('验证身份以传输文件')) return;
        if (!mounted) return;
        await Navigator.push<void>(
          context,
          MaterialPageRoute(
            builder: (_) => FileBrowser(
              center: _transfers,
              connect: () => _reopenPairedFiles(computer)!(),
            ),
          ),
        );
        return;
      }
      final target = await appSheet<({String address, String password})>(
        context,
        (_) => const LanSignIn(files: true),
      );
      if (target == null || !mounted) return;
      if ((_services.settings.locked || computer?.confirmOnHost == false) &&
          !await _authorize('验证身份以传输文件')) {
        return;
      }
      if (!mounted) return;
      await Navigator.push<void>(
        context,
        MaterialPageRoute(
          builder: (_) => FileBrowser(
            center: _transfers,
            connect: () => _transfers.connect(target.address, target.password),
          ),
        ),
      );
    } finally {
      _connecting = false;
    }
  }

  Future<bool> _authorize(String reason) async {
    final confirmed = await kConfirm(
      context,
      title: '验证身份',
      message: '$reason。请使用面容 ID 或指纹。',
      confirm: '继续验证',
    );
    if (confirmed != true || !mounted) return false;
    final authorized = await widget.auth(reason);
    if (!authorized && mounted) {
      KNotice.show(
        context,
        '生物识别未通过',
        detail: '请重试，或在系统设置中录入面容或指纹。',
        kind: KNoticeKind.error,
      );
    }
    return authorized;
  }

  Future<void> _scan() async {
    FocusScope.of(context).unfocus();
    final input = await Navigator.push<String>(
      context,
      MaterialPageRoute(
        builder: (_) => ScanPage(
          camera: _services.camera,
          preview: _services.preview,
          demoCode: _services.pairing is DemoPairingService,
        ),
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
        setState(() => _tab = 0);
        KNotice.show(
          context,
          '已配对',
          detail: '${request.computer.name} 已加入设备列表',
          kind: KNoticeKind.success,
        );
      } else if (result != null) {
        await _scan();
      }
    } on FormatException catch (error) {
      if (mounted) {
        KNotice.show(context, error.message, kind: KNoticeKind.error);
      }
    } on Exception catch (error) {
      if (mounted) {
        KNotice.show(
          context,
          error is PairFailure ? error.message : '无法请求配对，请检查电脑是否在线',
          kind: KNoticeKind.error,
        );
      }
    }
  }

  Future<void> _add() async {
    FocusScope.of(context).unfocus();
    final action = await appSheet<bool>(
      context,
      (context) => KOptionGroup(
        title: '添加电脑',
        children: [
          KRow(
            title: '扫码配对',
            leading: Icon(CupertinoIcons.viewfinder, color: context.action),
            chevron: true,
            onTap: () => Navigator.pop(context, true),
          ),
          KRow(
            title: '局域网地址 + 密码',
            leading: Icon(CupertinoIcons.link, color: context.action),
            chevron: true,
            onTap: () => Navigator.pop(context, false),
          ),
        ],
      ),
      grouped: true,
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
      reopenFiles: () => _transfers.connect(address, password),
    );
  }

  Future<void> _connect(Computer computer, {bool viewOnly = false}) async {
    if (_connecting) return;
    _connecting = true;
    FocusScope.of(context).unfocus();
    try {
      if (_services.settings.locked ||
          _services.sessions is DemoRemoteSessions && !computer.confirmOnHost) {
        if (!await _authorize('验证身份以连接「${computer.name}」')) return;
      }
      if (!mounted) return;
      await _showRemote(
        computer,
        _services.sessions.paired(computer),
        viewOnly: viewOnly,
        reopenFiles: _reopenPairedFiles(computer),
      );
    } finally {
      _connecting = false;
    }
  }

  Future<TransferConnection> Function()? _reopenPairedFiles(Computer computer) {
    final sessions = _services.sessions;
    if (sessions is! PairedRemoteSessions) return null;
    return () => _transfers.connectPaired(
      computer.id,
      computer.name,
      sessions.connection(computer),
    );
  }

  Future<void> _showRemote(
    Computer computer,
    RemoteSession session, {
    required bool viewOnly,
    Future<TransferConnection> Function()? reopenFiles,
  }) {
    TransferConnection? files;
    return Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => RemotePage(
          computer: computer,
          session: session,
          settings: _services.settings,
          history: _services.history,
          viewOnly: viewOnly,
          fileCenter: _transfers,
          connectFiles: session is LanRemoteSession
              ? () async => files ??= TransferConnection(
                  address: session.address,
                  service: await session.files,
                  reopen: reopenFiles,
                )
              : null,
        ),
      ),
    );
  }

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
      extendBody: true,
      body: SafeArea(
        bottom: false,
        child: IndexedStack(
          index: _tab,
          children: [
            DevicesPage(
              devices: _services.devices,
              scan: _scan,
              add: _add,
              connect: _connect,
              transfer: _connectFiles,
            ),
            TransfersPage(
              history: _services.history,
              devices: _services.devices,
              files: _transfers,
              browse: _connectFiles,
            ),
            SettingsPage(services: _services, authenticate: _authorize),
          ],
        ),
      ),
      bottomNavigationBar: ClipRect(
        child: BackdropFilter(
          filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
          child: ColoredBox(
            color: context.card.withValues(alpha: .82),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const KRule(),
                SafeArea(
                  top: false,
                  child: Row(
                    children: [
                      _tabItem(
                        0,
                        '设备',
                        CupertinoIcons.device_desktop,
                        'devices-tab',
                      ),
                      _tabItem(
                        1,
                        '传输',
                        CupertinoIcons.arrow_right_arrow_left,
                        'transfers-tab',
                      ),
                      _tabItem(2, '我的', CupertinoIcons.person, 'mine-tab'),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );

  Widget _tabItem(int index, String title, IconData icon, String key) {
    final color = _tab == index ? context.action : context.muted;
    return Expanded(
      child: Semantics(
        key: ValueKey(key),
        selected: _tab == index,
        child: KPress(
          onTap: () {
            FocusScope.of(context).unfocus();
            setState(() => _tab = index);
          },
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 7),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(icon, color: color, size: 24),
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
