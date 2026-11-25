import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'lan_session.dart';

void main() => runApp(const KkdeskApp());

class KkdeskApp extends StatelessWidget {
  const KkdeskApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'kkdesk',
      theme: ThemeData(colorSchemeSeed: const Color(0xff1479ff)),
      home: const _ConnectPage(),
    );
  }
}

class _ConnectPage extends StatefulWidget {
  const _ConnectPage();

  @override
  State<_ConnectPage> createState() => _ConnectPageState();
}

class _ConnectPageState extends State<_ConnectPage> {
  final _address = TextEditingController();
  final _password = TextEditingController();

  @override
  void initState() {
    super.initState();
    if (const bool.fromEnvironment('KKDESK_SPIKE_METRICS')) {
      _address.text = const String.fromEnvironment('KKDESK_SPIKE_ADDRESS');
      _password.text = const String.fromEnvironment('KKDESK_SPIKE_PASSWORD');
      if (_address.text.isNotEmpty && _password.text.isNotEmpty) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _connect();
        });
      }
    }
  }

  @override
  void dispose() {
    _address.dispose();
    _password.dispose();
    super.dispose();
  }

  void _connect() {
    if (_address.text.trim().isEmpty || _password.text.isEmpty) return;
    FocusScope.of(context).unfocus();
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => _RemotePage(
          address: _address.text.trim(),
          password: _password.text,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('kkdesk')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: ListView(
              shrinkWrap: true,
              padding: const EdgeInsets.all(24),
              children: [
                Text(
                  '局域网连接',
                  style: Theme.of(context).textTheme.headlineMedium,
                ),
                const SizedBox(height: 24),
                TextField(
                  controller: _address,
                  decoration: const InputDecoration(
                    labelText: '电脑地址',
                    hintText: '192.168.1.10:21118',
                    border: OutlineInputBorder(),
                  ),
                  autocorrect: false,
                  keyboardType: TextInputType.url,
                  textInputAction: TextInputAction.next,
                ),
                const SizedBox(height: 16),
                TextField(
                  controller: _password,
                  decoration: const InputDecoration(
                    labelText: '连接密码',
                    border: OutlineInputBorder(),
                  ),
                  obscureText: true,
                  onSubmitted: (_) => _connect(),
                ),
                const SizedBox(height: 24),
                FilledButton(onPressed: _connect, child: const Text('连接')),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _RemotePage extends StatefulWidget {
  const _RemotePage({required this.address, required this.password});

  final String address;
  final String password;

  @override
  State<_RemotePage> createState() => _RemotePageState();
}

class _RemotePageState extends State<_RemotePage> {
  final _renderer = RTCVideoRenderer();
  late final LanSession _session;
  late final Future<void> _initialize;
  String? _error;
  bool _hasVideo = false;
  int? _pointer;

  @override
  void initState() {
    super.initState();
    _renderer.onFirstFrameRendered = () {
      if (mounted) setState(() => _hasVideo = true);
    };
    _renderer.onResize = () {
      if (mounted) setState(() {});
    };
    _session = LanSession(
      onStream: (stream) => _renderer.srcObject = stream,
      onEnded: _onEnded,
    );
    _initialize = _renderer.initialize();
    unawaited(_connect());
  }

  Future<void> _connect() async {
    try {
      await _initialize;
      if (mounted) {
        await _session.connect(widget.address, widget.password);
      }
    } catch (error) {
      _onEnded('连接失败：$error');
    }
  }

  void _onEnded(String reason) {
    if (!mounted || _error != null) return;
    setState(() => _error = reason);
    unawaited(_session.close());
  }

  @override
  void dispose() {
    _renderer.onFirstFrameRendered = null;
    _renderer.onResize = null;
    unawaited(_session.close());
    unawaited(_disposeRenderer());
    super.dispose();
  }

  Future<void> _disposeRenderer() async {
    try {
      await _initialize;
    } catch (_) {
      return;
    }
    await _renderer.dispose();
  }

  Widget _video(BoxConstraints constraints) {
    final ratio = _renderer.videoWidth / _renderer.videoHeight;
    if (!ratio.isFinite || ratio <= 0) return RTCVideoView(_renderer);
    final width = min(constraints.maxWidth, constraints.maxHeight * ratio);
    final height = width / ratio;
    Offset normalized(PointerEvent event) => Offset(
      (event.localPosition.dx / width).clamp(0.0, 1.0),
      (event.localPosition.dy / height).clamp(0.0, 1.0),
    );
    void end(PointerEvent event) {
      if (_pointer != event.pointer) return;
      _pointer = null;
      final position = normalized(event);
      _session.button(position.dx, position.dy, false);
    }

    return Center(
      child: SizedBox(
        width: width,
        height: height,
        child: Listener(
          behavior: HitTestBehavior.opaque,
          onPointerDown: (event) {
            if (_pointer != null || !_session.inputReady) return;
            _pointer = event.pointer;
            final position = normalized(event);
            _session.button(position.dx, position.dy, true);
          },
          onPointerMove: (event) {
            if (_pointer != event.pointer) return;
            final position = normalized(event);
            _session.move(position.dx, position.dy);
          },
          onPointerUp: end,
          onPointerCancel: end,
          child: RTCVideoView(_renderer),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text(widget.address),
        foregroundColor: Colors.white,
        backgroundColor: Colors.black,
      ),
      body: SafeArea(
        child: _error != null
            ? Center(
                child: Text(
                  _error!,
                  style: const TextStyle(color: Colors.white),
                ),
              )
            : Stack(
                fit: StackFit.expand,
                children: [
                  LayoutBuilder(
                    builder: (_, constraints) => _video(constraints),
                  ),
                  if (!_hasVideo)
                    const Center(
                      child: Text(
                        '等待电脑画面…',
                        style: TextStyle(color: Colors.white),
                      ),
                    ),
                ],
              ),
      ),
    );
  }
}
