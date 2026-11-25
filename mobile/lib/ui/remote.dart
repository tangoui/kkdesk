import 'dart:async';
import 'dart:math' as math;
import 'dart:math' show Point;
import 'dart:ui';

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../activity_history.dart';
import '../mobile_services.dart';
import '../touch_controller.dart';
import 'common.dart';
import 'remote_keyboard.dart';

class RemotePage extends StatefulWidget {
  const RemotePage({
    super.key,
    required this.computer,
    required this.session,
    required this.settings,
    required this.history,
    this.viewOnly = false,
  });

  final Computer computer;
  final RemoteSession session;
  final MobileSettings settings;
  final ActivityHistory history;
  final bool viewOnly;

  @override
  State<RemotePage> createState() => _RemotePageState();
}

enum _Panel { menu, touch, screens, quality, more }

class _RemotePageState extends State<RemotePage> with WidgetsBindingObserver {
  late final _keys = RemoteKeys(widget.session);
  late final _touch = TouchController(
    (command) => widget.session.pointer(command, _keys.modifiers),
  )..mode = widget.settings.touchMode(widget.computer.id);
  final _input = FocusNode();
  final _surface = ValueNotifier(0);
  Timer? _hold;
  DateTime? _connectedAt;
  final _connectedClock = Stopwatch();
  ConnectionPath? _connectionPath;
  Timer? _captionTimer;
  Timer? _imeDismiss;
  _Panel? _panel;
  String? _caption;
  bool _keyboard = false;
  bool _imeWasVisible = false;
  bool _draggingBall = false;
  bool _right = true;
  double _ballY = .5;
  double? _ballX;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.session.addListener(_sessionChanged);
    unawaited(_enter());
    unawaited(widget.session.start());
    if (!widget.viewOnly && !widget.settings.hasSeenGestures) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && ModalRoute.of(context)?.isCurrent == true) {
          unawaited(_instructions());
        }
      });
    }
  }

  Future<void> _enter() async {
    await SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
    if (!mounted) return;
    await _setImmersive(true);
  }

  Future<void> _setImmersive(bool active) async {
    if (defaultTargetPlatform == TargetPlatform.android) {
      try {
        await const MethodChannel('kkdesk/display')
            .invokeMethod<void>('setImmersive', active);
        return;
      } on MissingPluginException {
        // Widget tests have no platform activity.
      }
    }
    await SystemChrome.setEnabledSystemUIMode(
      active ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
    );
  }

  Future<void> _instructions() async {
    await kDialog<void>(
      context,
      builder: (context) => KDialog(
        title: '远程手势',
        content: const Text(
          '触控板：单指移动光标，轻点左键。双指轻点右键、滑动滚动。按住再滑动可拖拽。\n\n'
          '直接点按：轻点左键，长按右键。双指捏合缩放，双指滑动平移画面或滚动。\n\n'
          '点开悬浮球可切换模式，或再次打开说明。',
          textAlign: TextAlign.start,
        ),
        actions: [
          KButton(
            key: const ValueKey('gestures-done'),
            label: '知道了',
            onTap: () => Navigator.pop(context),
          ),
        ],
      ),
    );
    if (mounted) await widget.settings.seeGestures();
  }

  void _sessionChanged() {
    if (!mounted) return;
    final status = widget.session.status;
    if (status.phase == RemotePhase.connected) {
      _connectedAt ??= DateTime.now();
      _connectedClock.start();
      _connectionPath = status.path;
    }
    if (status.phase == RemotePhase.ended) {
      _recordConnection();
      _touch.cancel();
      _closeKeyboard();
    }
    setState(() {});
  }

  void _recordConnection() {
    final start = _connectedAt;
    final path = _connectionPath;
    if (start == null || path == null) return;
    _connectedAt = null;
    _connectedClock.stop();
    final entry = (
      computer: widget.computer.name,
      deviceId: widget.computer.id,
      os: widget.computer.os,
      start: start,
      duration: _connectedClock.elapsed,
      path: path.label,
    );
    final history = widget.history;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      unawaited(history.recordConnection(entry));
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused) {
      _hold?.cancel();
      _touch.cancel();
      _keys.release();
      if (mounted && _keyboard) _closeKeyboard();
    }
  }

  @override
  void didChangeMetrics() {
    if (!_keyboard) return;
    _imeDismiss?.cancel();
    final view = View.of(context);
    if (view.viewInsets.bottom > 0) {
      _imeWasVisible = true;
    } else if (_imeWasVisible) {
      _imeDismiss = Timer(const Duration(milliseconds: 320), () {
        if (mounted && _keyboard && View.of(context).viewInsets.bottom == 0) {
          _closeKeyboard();
        }
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _hold?.cancel();
    _captionTimer?.cancel();
    _imeDismiss?.cancel();
    _touch.cancel();
    _keys.release();
    _surface.dispose();
    _input.dispose();
    widget.session.removeListener(_sessionChanged);
    _recordConnection();
    unawaited(_leave());
    super.dispose();
  }

  Future<void> _leave() async {
    await SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
    await _setImmersive(false);
    await widget.session.close();
  }

  void _brief(String text) {
    _captionTimer?.cancel();
    setState(() => _caption = text);
    _captionTimer = Timer(const Duration(seconds: 2), () {
      if (mounted) setState(() => _caption = null);
    });
  }

  void _setMode(TouchMode mode) {
    setState(() {
      _touch.mode = mode;
      _panel = null;
    });
    unawaited(widget.settings.setTouchMode(widget.computer.id, mode));
    _brief(mode == TouchMode.trackpad ? '已切换到触控板模式' : '已切换到直接点按模式');
  }

  void _openKeyboard() {
    setState(() {
      _keyboard = true;
      _imeWasVisible = false;
      _panel = null;
    });
    unawaited(_setImmersive(false));
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _keyboard) _input.requestFocus();
    });
  }

  void _closeKeyboard() {
    if (!_keyboard) return;
    _imeDismiss?.cancel();
    _keys.release();
    _input.unfocus();
    setState(() {
      _keyboard = false;
      _imeWasVisible = false;
    });
    unawaited(_setImmersive(true));
  }

  Point<double> _point(Offset offset) => Point(offset.dx, offset.dy);

  void _pointerDown(PointerDownEvent event) {
    if (widget.viewOnly ||
        widget.session.status.phase != RemotePhase.connected) {
      return;
    }
    _touch.down(event.pointer, _point(event.localPosition));
    _hold?.cancel();
    if (_touch.canHold) {
      _hold = Timer(const Duration(milliseconds: 480), _touch.hold);
    }
  }

  void _pointerMove(PointerMoveEvent event) {
    _touch.move(event.pointer, _point(event.localPosition));
    if (!_touch.canHold) _hold?.cancel();
    _surface.value++;
  }

  void _pointerUp(PointerUpEvent event) {
    _hold?.cancel();
    _touch.up(event.pointer);
  }

  void _pointerCancel(PointerCancelEvent event) {
    _hold?.cancel();
    _touch.cancel();
  }

  @override
  Widget build(BuildContext context) => PopScope<void>(
    canPop: _panel == null && !_keyboard,
    onPopInvokedWithResult: (didPop, _) {
      if (didPop) return;
      if (_panel != null) {
        setState(() => _panel = null);
      } else {
        _closeKeyboard();
      }
    },
    child: AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.light,
      child: Scaffold(
        backgroundColor: Colors.black,
        resizeToAvoidBottomInset: false,
        body: LayoutBuilder(
          builder: (context, bounds) {
            final media = MediaQuery.of(context);
            final size = bounds.biggest;
            final safe = media.viewPadding;
            final keyboardBottom = _keyboard
                ? math.max(media.viewInsets.bottom, safe.bottom)
                : 0.0;
            final displayBottom = _keyboard ? keyboardBottom + 47 : 0.0;
            return Stack(
              fit: StackFit.expand,
              children: [
                Positioned.fill(
                  bottom: displayBottom,
                  child: _desktop(size.height),
                ),
                if (_panel != null)
                  Positioned.fill(
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => setState(() => _panel = null),
                    ),
                  ),
                if (widget.session.status.phase != RemotePhase.ended &&
                    !_keyboard)
                  ..._floating(size, safe),
                if (_caption case final caption?)
                  Positioned(
                    top: safe.top + 12,
                    left: safe.left + 15,
                    right: safe.right + 15,
                    child: IgnorePointer(
                      child: Center(child: _Caption(caption)),
                    ),
                  ),
                if (widget.session.status.phase != RemotePhase.connected)
                  _connectionOverlay(),
                if (widget.viewOnly &&
                    widget.session.status.phase == RemotePhase.connected)
                  Positioned(
                    top: safe.top + 9,
                    left: safe.left + 11,
                    child: const _Caption('仅查看'),
                  ),
                if (_keyboard) ...[
                  Positioned(
                    left: 0,
                    top: 0,
                    child: RemoteTextInput(
                      keys: _keys,
                      focus: _input,
                      onUnsupported: (_) => _brief('当前远程键盘支持 USB HID 字符'),
                    ),
                  ),
                  Positioned(
                    left: safe.left,
                    right: safe.right,
                    bottom: keyboardBottom,
                    child: TextFieldTapRegion(
                      child: Focus(
                        canRequestFocus: false,
                        descendantsAreFocusable: false,
                        child: RemoteKeyboardBar(
                          key: const ValueKey('shortcut-bar'),
                          keys: _keys,
                          os: widget.computer.os,
                          changed: () => setState(() {}),
                          close: _closeKeyboard,
                        ),
                      ),
                    ),
                  ),
                ],
              ],
            );
          },
        ),
      ),
    ),
  );

  Widget _desktop(double fullHeight) => LayoutBuilder(
    builder: (context, constraints) {
      final width = constraints.maxWidth;
      final height = constraints.maxHeight;
      if (width <= 0 || height <= 0) return const SizedBox.shrink();
      final ratio = widget.session.aspectRatio;
      final fitWidth = math.min(width, fullHeight * ratio);
      _touch.layout(Point(width, height), Point(fitWidth, fitWidth / ratio));
      return Listener(
        behavior: HitTestBehavior.opaque,
        onPointerDown: _pointerDown,
        onPointerMove: _pointerMove,
        onPointerUp: _pointerUp,
        onPointerCancel: _pointerCancel,
        child: ValueListenableBuilder(
          valueListenable: _surface,
          child: widget.session.video(),
          builder: (context, _, video) {
            final origin = _touch.origin;
            final size = _touch.size;
            final cursor = _touch.screenCursor;
            return ClipRect(
              child: Stack(
                children: [
                  Positioned(
                    left: origin.x,
                    top: origin.y,
                    width: size.x,
                    height: size.y,
                    key: const ValueKey('remote-video-frame'),
                    child: video!,
                  ),
                  if (!widget.viewOnly &&
                      _touch.mode == TouchMode.trackpad &&
                      widget.session.status.phase == RemotePhase.connected)
                    Positioned(
                      key: const ValueKey('trackpad-cursor'),
                      left: cursor.x,
                      top: cursor.y,
                      child: const IgnorePointer(
                        child: CustomPaint(
                          size: Size(17, 23),
                          painter: _Cursor(),
                        ),
                      ),
                    ),
                ],
              ),
            );
          },
        ),
      );
    },
  );

  Widget _connectionOverlay() {
    final status = widget.session.status;
    final ended = status.phase == RemotePhase.ended;
    return Center(
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: context.card,
          borderRadius: BorderRadius.circular(14),
        ),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(24, 18, 24, 13),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (!ended) const KLoader(radius: 11),
              if (!ended) const SizedBox(height: 13),
              Text(
                ended
                    ? (status.detail ?? '远程连接已断开')
                    : (widget.computer.os != ComputerOS.unknown &&
                              widget.computer.confirmOnHost
                          ? '等待电脑确认…'
                          : '正在连接…'),
                style: TextStyle(color: context.ink, fontSize: 15),
              ),
              KButton(
                variant: KButtonVariant.text,
                onTap: () => Navigator.pop(context),
                label: ended ? '返回设备' : '取消',
              ),
            ],
          ),
        ),
      ),
    );
  }

  Iterable<Widget> _floating(Size size, EdgeInsets safe) sync* {
    const diameter = 46.0;
    final left = safe.left + 10;
    final right = math.max(left, size.width - safe.right - diameter - 10);
    final top = safe.top + 7;
    final bottom = math.max(top, size.height - safe.bottom - diameter - 7);
    final x = (_ballX ?? (_right ? right : left)).clamp(left, right);
    final y = top + _ballY * (bottom - top);
    if (_panel != null) {
      final width = math.min(
        208.0,
        math.max(125.0, size.width - safe.horizontal - 92),
      );
      final menuHeight = switch (_panel!) {
        _Panel.menu => 288.0,
        _Panel.touch => 166.0,
        _Panel.more => 218.0,
        _Panel.screens => 41.0 * (widget.session.screens.length + 1) + 12.0,
        _Panel.quality => 176.0,
      };
      final maxHeight = math.min(
        menuHeight,
        math.max(90.0, size.height - safe.vertical - 16),
      );
      final menuTop = (y + diameter / 2 - maxHeight / 2).clamp(
        safe.top + 7,
        math.max(safe.top + 7, size.height - safe.bottom - maxHeight - 7),
      );
      yield Positioned(
        left: (_right ? x - width - 9 : x + diameter + 9).clamp(
          safe.left + 4,
          math.max(safe.left + 4, size.width - safe.right - width - 4),
        ),
        top: menuTop.toDouble(),
        width: width,
        child: _menu(maxHeight),
      );
    }
    yield AnimatedPositioned(
      duration: _draggingBall
          ? Duration.zero
          : const Duration(milliseconds: 190),
      curve: Curves.easeOutCubic,
      left: x,
      top: y,
      width: diameter,
      height: diameter,
      child: GestureDetector(
        key: const ValueKey('remote-ball'),
        onPanStart: (_) => setState(() {
          _draggingBall = true;
          _panel = null;
          _ballX = x;
        }),
        onPanUpdate: (details) => setState(() {
          _ballX = (_ballX! + details.delta.dx).clamp(left, right);
          if (bottom > top) {
            _ballY = (_ballY + details.delta.dy / (bottom - top)).clamp(
              0.0,
              1.0,
            );
          }
        }),
        onPanEnd: (_) => _snapBall(left, right),
        onPanCancel: () => _snapBall(left, right),
        onTap: () =>
            setState(() => _panel = _panel == null ? _Panel.menu : null),
        child: Semantics(
          button: true,
          label: _panel == null ? '展开远程工具' : '收起远程工具',
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: const Color(0xaf24252b),
              shape: BoxShape.circle,
              border: Border.all(color: Colors.white30),
              boxShadow: const [
                BoxShadow(
                  color: Colors.black45,
                  blurRadius: 7,
                  offset: Offset(0, 3),
                ),
              ],
            ),
            child: const Center(child: BrandMark(size: 27)),
          ),
        ),
      ),
    );
  }

  void _snapBall(double left, double right) {
    setState(() {
      _right = (_ballX ?? right) >= (left + right) / 2;
      _ballX = null;
      _draggingBall = false;
    });
  }

  Widget _menu(double maxHeight) => ClipRRect(
    borderRadius: BorderRadius.circular(18),
    child: BackdropFilter(
      filter: ImageFilter.blur(sigmaX: 14, sigmaY: 14),
      child: ColoredBox(
        color: context.card.withValues(alpha: .92),
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: maxHeight),
          child: SingleChildScrollView(
            padding: const EdgeInsets.symmetric(vertical: 5, horizontal: 9),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: _menuRows(),
            ),
          ),
        ),
      ),
    ),
  );

  List<Widget> _menuRows() {
    final session = widget.session;
    final connected = session.status.phase == RemotePhase.connected;
    final touchAllowed = connected && !widget.viewOnly;
    final trackpad = _touch.mode == TouchMode.trackpad;
    switch (_panel!) {
      case _Panel.menu:
        return [
          _ConnectionStatus(session.status),
          const KRule(),
          _row(
            '键盘',
            CupertinoIcons.keyboard,
            key: 'remote-keyboard',
            onTap: touchAllowed ? _openKeyboard : null,
          ),
          _row(
            trackpad ? '触控板' : '直接点按',
            CupertinoIcons.hand_draw,
            key: 'remote-mode',
            trailing: '切换',
            onTap: touchAllowed
                ? () => setState(() => _panel = _Panel.touch)
                : null,
          ),
          _row(
            '屏幕',
            CupertinoIcons.rectangle_on_rectangle,
            trailing: '${session.screen + 1} / ${session.screens.length}',
            onTap: connected && session.screens.length > 1
                ? () => setState(() => _panel = _Panel.screens)
                : null,
          ),
          _row(
            '画质',
            CupertinoIcons.slider_horizontal_3,
            trailing: session.quality.label,
            onTap: connected && session.canChangeQuality
                ? () => setState(() => _panel = _Panel.quality)
                : null,
          ),
          _row(
            '更多',
            CupertinoIcons.ellipsis,
            trailing: '›',
            onTap: connected
                ? () => setState(() => _panel = _Panel.more)
                : null,
          ),
          _row(
            '断开',
            CupertinoIcons.power,
            key: 'remote-disconnect',
            color: const Color(0xffff746d),
            onTap: () => Navigator.pop(context),
          ),
        ];
      case _Panel.touch:
        return [
          _backRow('触控模式'),
          _row(
            '触控板',
            CupertinoIcons.cursor_rays,
            trailing: trackpad ? '✓' : null,
            onTap: () => _setMode(TouchMode.trackpad),
          ),
          _row(
            '直接点按',
            CupertinoIcons.hand_draw,
            key: 'mode-direct',
            trailing: !trackpad ? '✓' : null,
            onTap: () => _setMode(TouchMode.direct),
          ),
          _row(
            '手势说明',
            CupertinoIcons.info,
            onTap: () {
              setState(() => _panel = null);
              unawaited(_instructions());
            },
          ),
        ];
      case _Panel.screens:
        return [
          _backRow('屏幕'),
          for (var index = 0; index < session.screens.length; index++)
            _row(
              session.screens[index],
              CupertinoIcons.device_desktop,
              trailing: index == session.screen ? '✓' : null,
              onTap: () {
                session.setScreen(index);
                setState(() => _panel = null);
              },
            ),
        ];
      case _Panel.quality:
        return [
          _backRow('画质'),
          for (final quality in RemoteQuality.values)
            _row(
              quality.label,
              CupertinoIcons.slider_horizontal_3,
              trailing: quality == session.quality ? '✓' : null,
              onTap: () {
                session.setQuality(quality);
                setState(() => _panel = null);
              },
            ),
        ];
      case _Panel.more:
        return [
          _backRow('更多'),
          _row(
            'Ctrl+Alt+Del',
            CupertinoIcons.command,
            onTap: touchAllowed
                ? () {
                    _keys.chord([2, 4], 0x4c);
                    setState(() => _panel = null);
                  }
                : null,
          ),
          _row(
            '锁屏',
            CupertinoIcons.lock,
            onTap: touchAllowed && widget.computer.os != ComputerOS.unknown
                ? () {
                    if (widget.computer.os == ComputerOS.mac) {
                      _keys.chord([2, 8], 0x14);
                    } else {
                      _keys.chord([8], 0x0f);
                    }
                    setState(() => _panel = null);
                  }
                : null,
          ),
          _row(
            '剪贴板',
            CupertinoIcons.doc_on_clipboard,
            onTap: touchAllowed && session.canUseClipboard ? _clipboard : null,
          ),
          _row('隐私屏', CupertinoIcons.eye_slash),
        ];
    }
  }

  Widget _backRow(String title) => _row(
    title,
    CupertinoIcons.chevron_left,
    onTap: () => setState(() => _panel = _Panel.menu),
  );

  Widget _row(
    String label,
    IconData icon, {
    String? key,
    String? trailing,
    Color? color,
    VoidCallback? onTap,
  }) => KPress(
    key: key == null ? null : ValueKey(key),
    onTap: onTap,
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 5),
      child: SizedBox(
        height: 39,
        child: Row(
          children: [
            Icon(
              icon,
              size: 17,
              color: onTap == null ? context.muted : color ?? context.ink,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                label,
                maxLines: 1,
                style: TextStyle(
                  fontSize: 14,
                  color: onTap == null ? context.muted : color ?? context.ink,
                ),
              ),
            ),
            if (trailing != null)
              Text(
                trailing,
                style: TextStyle(color: context.muted, fontSize: 12),
              ),
          ],
        ),
      ),
    ),
  );

  Future<void> _clipboard() async {
    setState(() => _panel = null);
    final send = await appSheet<bool>(
      context,
      (context) => KOptionGroup(
        title: '剪贴板',
        children: [
          KRow(title: '复制到手机', onTap: () => Navigator.pop(context, false)),
          KRow(title: '发送到电脑', onTap: () => Navigator.pop(context, true)),
        ],
      ),
      grouped: true,
    );
    if (send == null || !mounted) return;
    try {
      final String text;
      if (send) {
        final item = await Clipboard.getData(Clipboard.kTextPlain);
        if (!mounted) return;
        text = item?.text ?? '';
        await widget.session.writeClipboard(text);
      } else {
        text = await widget.session.readClipboard();
        if (!mounted) return;
        await Clipboard.setData(ClipboardData(text: text));
      }
      if (!mounted) return;
      widget.history.recordClipboard((
        computer: widget.computer.name,
        deviceId: widget.computer.id,
        os: widget.computer.os,
        time: DateTime.now(),
        text: text,
      ));
      KNotice.show(
        context,
        send ? '已发送到电脑剪贴板' : '已复制到手机剪贴板',
        kind: KNoticeKind.success,
      );
    } on Exception {
      if (mounted) KNotice.show(context, '无法访问剪贴板', kind: KNoticeKind.error);
    }
  }
}

class _ConnectionStatus extends StatelessWidget {
  const _ConnectionStatus(this.status);
  final RemoteStatus status;

  @override
  Widget build(BuildContext context) {
    final connected = status.phase == RemotePhase.connected;
    final latency = status.latency;
    final fps = status.fps;
    final label = connected
        ? '${latency == null ? '—' : '$latency ms'} · ${status.path.label} · ${fps == null ? '—' : '$fps 帧'}'
        : '正在连接…';
    return SizedBox(
      height: 37,
      child: Row(
        children: [
          const SizedBox(width: 5),
          Container(
            width: 7,
            height: 7,
            decoration: BoxDecoration(
              color: !connected || status.path == ConnectionPath.relay
                  ? amber
                  : green,
              shape: BoxShape.circle,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              label,
              style: TextStyle(color: context.muted, fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}

class _Caption extends StatelessWidget {
  const _Caption(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      color: const Color(0xeeffffff),
      borderRadius: BorderRadius.circular(24),
      boxShadow: const [BoxShadow(color: Colors.black26, blurRadius: 10)],
    ),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 15, vertical: 8),
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: const TextStyle(
          fontWeight: FontWeight.w700,
          color: Color(0xff18181b),
          fontSize: 13,
        ),
      ),
    ),
  );
}

class _Cursor extends CustomPainter {
  const _Cursor();

  @override
  void paint(Canvas canvas, Size size) {
    final path = Path()
      ..moveTo(1, 1)
      ..lineTo(15, 13)
      ..lineTo(9.5, 13)
      ..lineTo(13, 19)
      ..lineTo(10, 21)
      ..lineTo(6, 14)
      ..lineTo(2, 18)
      ..close();
    canvas.drawShadow(path, Colors.black, 2, true);
    canvas.drawPath(path, Paint()..color = Colors.white);
    canvas.drawPath(
      path,
      Paint()
        ..color = const Color(0xff19191b)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.4
        ..strokeJoin = StrokeJoin.round,
    );
  }

  @override
  bool shouldRepaint(_Cursor oldDelegate) => false;
}
