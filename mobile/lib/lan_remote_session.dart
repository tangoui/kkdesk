import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'lan_session.dart';
import 'mobile_services.dart';
import 'touch_controller.dart';

class LanRemoteSession extends ChangeNotifier implements RemoteSession {
  LanRemoteSession(this.address, this._password);

  final String address;
  final String _password;
  final _renderer = RTCVideoRenderer();
  late final LanSession _session = LanSession(
    onStream: _onStream,
    onEnded: _onEnded,
  );
  RemoteStatus _status = const RemoteStatus();
  Timer? _stats;
  Timer? _frameTimeout;
  Future<void>? _initializing;
  bool _closed = false;

  @override
  RemoteStatus get status => _status;
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
      await _session.connect(address, _password);
      if (_closed || status.phase != RemotePhase.connecting) return;
      _frameTimeout = Timer(const Duration(seconds: 20), () {
        _onEnded('等待远程画面超时');
        unawaited(_session.close());
      });
    } catch (error) {
      _onEnded(
        error is FormatException
            ? error.message
            : error is StateError
            ? error.message
            : '无法连接电脑，请检查地址、密码和局域网连接。',
      );
    }
  }

  void _onStream(MediaStream stream) {
    if (!_closed) _renderer.srcObject = stream;
  }

  void _onFirstFrame() {
    if (_closed || status.phase != RemotePhase.connecting) return;
    _frameTimeout?.cancel();
    _status = const RemoteStatus(phase: RemotePhase.connected);
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
        latency: stats.latency,
        fps: stats.fps,
      );
      notifyListeners();
    } on Exception {
      // The previous sample remains visible if a stats request fails.
    }
  }

  void _onEnded(String reason) {
    if (_closed || status.phase == RemotePhase.ended) return;
    _stats?.cancel();
    _frameTimeout?.cancel();
    _status = RemoteStatus(phase: RemotePhase.ended, detail: reason);
    notifyListeners();
  }

  @override
  void pointer(PointerCommand command, int modifiers) {
    if (_closed || status.phase != RemotePhase.connected) return;
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
    if (!_closed && status.phase == RemotePhase.connected) {
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
      await _session.close();
    } finally {
      try {
        await _renderer.dispose();
      } finally {
        dispose();
      }
    }
  }
}
