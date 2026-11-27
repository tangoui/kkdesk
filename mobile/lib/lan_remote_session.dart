import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'desktop_status.dart';
import 'files/file_transfer_service.dart';
import 'lan_auth.dart';
import 'lan_session.dart';
import 'mobile_services.dart';
import 'pairing/transport.dart';
import 'touch_controller.dart';

class LanRemoteSession extends ChangeNotifier implements DesktopRemoteSession {
  LanRemoteSession(this.address, this._password) : _paired = null;
  LanRemoteSession.paired(this.address, this._paired) : _password = '';

  final PairedConnection? _paired;
  ConnectionPath get _path =>
      _session.relay ? ConnectionPath.relay : ConnectionPath.direct;

  final String address;
  Future<FileTransferService> get files => _session.files;
  String? get firstHostCode => _session.firstHostCode;
  final String _password;
  final _renderer = RTCVideoRenderer();
  late final LanSession _session = LanSession(
    onStream: _onStream,
    onEnded: _onEnded,
    onAccessibilityChanged: notifyListeners,
    onDesktopChanged: notifyListeners,
  );
  RemoteStatus _status = const RemoteStatus();
  Timer? _stats;
  Timer? _frameTimeout;

  /// How long to wait for the first picture once connected. The computer
  /// may still be asking its user (up to 30 s, internal/app ConfirmTimeout,
  /// after which it closes the session itself) and then starts capture, so
  /// this has to outlast both.
  static const firstFrameTimeout = Duration(seconds: 45);
  Future<void>? _initializing;
  bool _closed = false;

  @override
  DesktopStatus get desktopStatus => _session.desktopStatus;
  @override
  bool get desktopReady => !_closed && _session.desktopReady;
  @override
  Future<bool> setPrivacyScreen(bool on) => _session.setPrivacyScreen(on);
  @override
  Future<bool> setLockAfterDisconnect(bool on) =>
      _session.setLockAfterDisconnect(on);
  @override
  Future<bool> lockAndDisconnect() => _session.lockAndDisconnect();

  @override
  RemoteStatus get status => RemoteStatus(
    phase: _status.phase,
    path: _status.path,
    latency: _status.latency,
    fps: _status.fps,
    detail: _status.detail,
    accessibilityDenied: _session.accessibilityDenied,
    firstHostCode: firstHostCode,
    identityChanged: _status.identityChanged,
  );
  @override
  double get aspectRatio =>
      _renderer.videoWidth > 0 && _renderer.videoHeight > 0
      ? _renderer.videoWidth / _renderer.videoHeight
      : 16 / 9;
  @override
  Widget video() => RTCVideoView(_renderer);
  @override
  int get screen => 0;
  @override
  List<String> get screens => const ['主屏幕'];
  @override
  RemoteQuality get quality => RemoteQuality.auto;
  @override
  bool get canChangeQuality => false;
  @override
  bool get canUseClipboard => false;

  @override
  Future<void> start() async {
    try {
      await (_initializing = _renderer.initialize());
      if (_closed) return;
      _renderer.onResize = () {
        if (!_closed) notifyListeners();
      };
      _renderer.onFirstFrameRendered = _onFirstFrame;
      if (_paired case final connect?) {
        await _session.connectPaired(address, connect);
      } else {
        await _session.connect(address, _password);
        if (!_closed && firstHostCode != null) notifyListeners();
      }
      if (_closed || status.phase != RemotePhase.connecting) return;
      _frameTimeout = Timer(firstFrameTimeout, () {
        _onEnded('等待远程画面超时');
        unawaited(_session.close());
      });
    } catch (error) {
      _onEnded(
        error is LanAuthFailure
            ? error.message
            : error is PairFailure
            ? error.message
            : error is FormatException
            ? error.message
            : error is StateError
            ? error.message
            : _paired == null
            ? '无法连接电脑，请检查地址、密码和局域网连接。'
            : '无法连接已配对的电脑，请检查电脑和跨网服务是否在线。',
        identityChanged: error is LanIdentityChanged ? error : null,
      );
    }
  }

  void _onStream(MediaStream stream) {
    if (!_closed) _renderer.srcObject = stream;
  }

  void _onFirstFrame() {
    if (_closed || status.phase != RemotePhase.connecting) return;
    _frameTimeout?.cancel();
    _status = RemoteStatus(phase: RemotePhase.connected, path: _path);
    _stats = Timer.periodic(
      const Duration(seconds: 1),
      (_) => unawaited(_refreshStats()),
    );
    notifyListeners();
  }

  Future<void> _refreshStats() async {
    try {
      final stats = await _session.mobileStats();
      if (_closed || status.phase != RemotePhase.connected) return;
      _status = RemoteStatus(
        phase: RemotePhase.connected,
        path: _path,
        latency: stats.latency,
        fps: stats.fps,
      );
      notifyListeners();
    } on Exception {
      // The previous sample remains visible if a stats request fails.
    }
  }

  void _onEnded(String reason, {LanIdentityChanged? identityChanged}) {
    if (_closed || status.phase == RemotePhase.ended) return;
    _stats?.cancel();
    _frameTimeout?.cancel();
    _status = RemoteStatus(
      phase: RemotePhase.ended,
      detail: reason,
      identityChanged: identityChanged,
    );
    notifyListeners();
  }

  @override
  void pointer(PointerCommand command, int modifiers) {
    if (_closed ||
        _status.phase != RemotePhase.connected ||
        _session.accessibilityDenied) {
      return;
    }
    switch (command) {
      case PointerMove(:final position, :final dragging):
        if (dragging) {
          _session.sendDragMove(position.x, position.y);
        } else {
          _session.move(position.x, position.y);
        }
      case PointerButton(:final position, :final button, :final down):
        _session.sendButton(
          position.x,
          position.y,
          button.index,
          down,
          modifiers,
        );
      case PointerWheel(:final delta):
        _session.sendWheel(delta.x, delta.y, modifiers);
    }
  }

  @override
  void key(int hid, bool down, int modifiers) {
    if (!_closed &&
        _status.phase == RemotePhase.connected &&
        !_session.accessibilityDenied) {
      _session.sendKey(hid, down, modifiers);
    }
  }

  @override
  void setScreen(int index) {}

  @override
  void setQuality(RemoteQuality quality) {}

  @override
  Future<String> readClipboard() async => throw UnsupportedError('局域网剪贴板');

  @override
  Future<void> writeClipboard(String text) async =>
      throw UnsupportedError('局域网剪贴板');

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _stats?.cancel();
    _frameTimeout?.cancel();
    try {
      await _initializing;
      _renderer.srcObject = null;
      await _session.disconnect();
    } finally {
      try {
        await _renderer.dispose();
      } finally {
        dispose();
      }
    }
  }
}
