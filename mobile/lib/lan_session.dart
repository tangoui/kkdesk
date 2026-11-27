import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'desktop_meta.dart';
import 'desktop_status.dart';
import 'files/file_transfer_service.dart';
import 'files/lan_file_transfer_service.dart';
import 'lan_auth.dart';
import 'lan_protocol.dart';
import 'pairing/transport.dart';

part 'lan_session_mobile_controls.dart';

const _exchangeTimeout = Duration(seconds: 12);
const _measure = bool.fromEnvironment('KKDESK_SPIKE_METRICS');
const _probeChannel = MethodChannel('kkdesk/spike');

typedef PairedConnection = Future<void> Function(
  Future<void> Function(AuthenticatedPair pair) negotiate,
);

enum LanSessionMode { desktop, files }

class LanSession {
  LanSession({
    required this.onStream,
    required this.onEnded,
    this.onAccessibilityChanged,
    this.onDesktopChanged,
    this.onUnlockResult,
  });

  final void Function(MediaStream stream) onStream;
  final void Function(String message) onEnded;
  final VoidCallback? onAccessibilityChanged;
  final VoidCallback? onDesktopChanged;
  final void Function(UnlockResult)? onUnlockResult;
  bool _accessibilityDenied = false;
  bool get accessibilityDenied => _accessibilityDenied;

  void _setAccessibilityDenied(bool denied) {
    if (_accessibilityDenied == denied) return;
    _accessibilityDenied = denied;
    if (!_closed) onAccessibilityChanged?.call();
  }

  final _clock = ClockSync();
  final _captures = <int, int>{};
  RTCPeerConnection? _peer;
  Socket? _socket;
  RTCDataChannel? _move;
  RTCDataChannel? _input;
  RTCDataChannel? _meta;
  LanFileTransferService? _files;
  Timer? _ping;
  Timer? _stats;
  bool _closed = false;
  bool _frameProbe = false;
  PairTransport? _pairTransport;
  Completer<bool>? _negotiated;
  bool relay = false;
  String? firstHostCode;
  _LanPasswordExchange? _passwordExchange;
  LanSessionMode? _mode;
  bool _pairAnswerVerified = false;
  bool _ended = false;
  bool _disconnecting = false;
  String? _hostKey;
  String? get hostKey => _hostKey;
  DesktopStatus _desktopStatus = const DesktopStatus();
  DesktopStatus get desktopStatus => _desktopStatus;
  final _initialDesktopUpdates = <Object, DesktopMeta>{};

  bool get desktopReady =>
      !_closed &&
      !_ended &&
      !_disconnecting &&
      _mode == LanSessionMode.desktop &&
      _hostKey != null &&
      _meta?.state == RTCDataChannelState.RTCDataChannelOpen;

  bool get inputReady =>
      _passwordExchange == null &&
      _input?.state == RTCDataChannelState.RTCDataChannelOpen;

  Future<FileTransferService> get files async {
    final service = _files;
    if (_closed || service == null) throw const FileTransferError('closed');
    return service.ready;
  }

  Future<void> connect(
    String address,
    String password, {
    LanSessionMode mode = LanSessionMode.desktop,
  }) async {
    final endpoint = passwordLanEndpoint(address);
    final identities = LanIdentities();
    final exchange = _LanPasswordExchange();
    _passwordExchange = exchange;

    try {
      final socket = await Socket.connect(
        endpoint.host,
        endpoint.port,
        timeout: _exchangeTimeout,
      );
      if (_closed) {
        socket.destroy();
        return;
      }
      _socket = socket;
      final messages = StreamIterator<String>(
        boundedLines(socket.cast<List<int>>()),
      );
      try {
        requireLanV2(await _receive(messages));
        final peer = await _offer(mode, address);
        final local = (await peer.getLocalDescription())!;
        final offer = local.sdp!;
        _send(socket, {'sdp': local.toMap(), 'mode': mode.name});
        final reply = await _receive(messages);
        final sdp = reply['sdp'];
        if (sdp is! Map<String, dynamic> ||
            sdp['type'] != 'answer' ||
            sdp['sdp'] is! String) {
          throw invalidLanIdentity;
        }
        final answer = sdp['sdp'] as String;
        final key = await verifyLanAnswer(
          offer,
          answer,
          reply['hostKey'],
          reply['hostSig'],
        );
        await identities.check(endpoint, key);
        if (_closed) throw const LanAuthFailure('连接已取消');
        await peer.setRemoteDescription(
          RTCSessionDescription(answer, 'answer'),
        );
        await exchange.authenticate(lanPasswordProof(password, offer, answer));
        if (_closed) throw const LanAuthFailure('连接已取消');
        firstHostCode = await identities.remember(endpoint, key);
        if (_closed) throw const LanAuthFailure('连接已取消');
        if (exchange.ended case final reason?) throw LanAuthFailure(reason);
        _files = LanFileTransferService(exchange.files, address);
        _passwordExchange = null;
        _admitDesktop(key);
        _startClock(exchange.meta);
        if (exchange.accessibility case final granted?) {
          _setAccessibilityDenied(!granted);
        }
        if (exchange.track case final track?) {
          _publishTrack(track.streams.first, track.track.id);
        }
        if (mode == LanSessionMode.files) await files;
        if (mode == LanSessionMode.desktop && _measure && !_closed) {
          _stats = Timer.periodic(const Duration(seconds: 1), (_) {
            unawaited(_logStats(peer));
          });
        }
      } finally {
        socket.destroy();
        _socket = null;
        await messages.cancel();
      }
    } catch (_) {
      await close();
      rethrow;
    }
  }

  Future<void> connectPaired(
    String name,
    PairedConnection connect, {
    LanSessionMode mode = LanSessionMode.desktop,
  }) async {
    await connect((pair) async {
      if (_closed) throw const PairFailure('连接已取消');
      final transport = pair.transport;
      _pairTransport = transport;
      relay = transport.relay;
      final ready = Completer<bool>();
      _negotiated = ready;
      try {
        final peer = await _offer(mode, name, {
          'iceServers': transport.ice,
          if (transport.relayOnly) 'iceTransportPolicy': 'relay',
        });
        final local = await peer.getLocalDescription();
        await pair.send({
          'type': 'offer',
          'mode': mode.name,
          'sdp': local!.sdp!,
        });
        final reply = await pair.receive(const Duration(seconds: 20));
        if (reply['type'] != 'answer' || reply['sdp'] is! String) {
          throw PairFailure(
            reply['status'] == 'busy' ? '电脑正在处理其他连接' : '电脑无法建立远程连接',
          );
        }
        _pairAnswerVerified = true;
        await peer.setRemoteDescription(
          RTCSessionDescription(reply['sdp'] as String, 'answer'),
        );
        if (!await ready.future.timeout(const Duration(seconds: 10))) {
          throw const SocketException('无法建立 WebRTC 连接');
        }
        if (_closed || _ended) throw const PairFailure('连接已取消');
        _admitDesktop(pair.transcript.hostKey);
        if (mode == LanSessionMode.files) await files;
      } catch (_) {
        await _resetPeer();
        rethrow;
      } finally {
        _pairTransport = null;
        _negotiated = null;
      }
    });
  }

  Future<void> _resetPeer() async {
    _ping?.cancel();
    _move = _input = _meta = null;
    _pairAnswerVerified = false;
    _hostKey = null;
    _initialDesktopUpdates.clear();
    _desktopStatus = const DesktopStatus();
    final files = _files;
    final peer = _peer;
    _files = null;
    _peer = null;
    _setAccessibilityDenied(false);
    if (peer != null) {
      peer.onConnectionState = null;
      peer.onTrack = null;
    }
    await files?.close();
    await peer?.dispose();
  }

  Future<RTCPeerConnection> _offer(
    LanSessionMode mode,
    String address, [
    Map<String, dynamic> configuration = const {'iceServers': <Object>[]},
  ]) async {
    final peer = await createPeerConnection(configuration);
    if (_closed) {
      await peer.dispose();
      throw StateError('连接已取消');
    }
    _peer = peer;
    _mode = mode;
    final exchange = _passwordExchange;
    peer.onTrack = (event) {
      if (!_closed && event.track.kind == 'video' && event.streams.isNotEmpty) {
        if (_passwordExchange != null) {
          exchange!.track = event;
          return;
        }
        _publishTrack(event.streams.first, event.track.id);
      }
    };
    peer.onConnectionState = (state) {
      switch (state) {
        case RTCPeerConnectionState.RTCPeerConnectionStateConnected:
          if (_negotiated case final pending? when !pending.isCompleted) {
            pending.complete(true);
          }
        case RTCPeerConnectionState.RTCPeerConnectionStateFailed:
        case RTCPeerConnectionState.RTCPeerConnectionStateClosed:
          if (_negotiated case final pending? when !pending.isCompleted) {
            pending.complete(false);
          } else if (_passwordExchange != null) {
            exchange!.stop('远程连接已断开');
          } else if (identical(_peer, peer)) {
            unawaited(_files?.close());
            _end('远程连接已断开');
          }
        default:
          break;
      }
    };

    final gather = Completer<void>();
    peer.onIceGatheringState = (state) {
      if (state == RTCIceGatheringState.RTCIceGatheringStateComplete &&
          !gather.isCompleted) {
        gather.complete();
      }
    };
    _move = await peer.createDataChannel('input-move', _UnreliableChannel());
    _input = await peer.createDataChannel('input', RTCDataChannelInit());
    final meta = await peer.createDataChannel('meta', RTCDataChannelInit());
    _meta = meta;
    final filesChannel = await peer.createDataChannel(
      'files',
      RTCDataChannelInit()..ordered = true,
    );
    if (exchange != null) {
      exchange
        ..channels = [_move!, _input!, meta, filesChannel]
        ..meta = meta
        ..files = filesChannel;
    } else {
      _files = LanFileTransferService(filesChannel, address);
    }
    if (_closed) throw const LanAuthFailure('连接已取消');
    meta.onDataChannelState = (state) {
      if (_closed || _peer != peer) return;
      _ping?.cancel();
      if (state == RTCDataChannelState.RTCDataChannelOpen &&
          _passwordExchange == null &&
          !_closed) {
        _startClock(meta);
      }
      if (_hostKey != null && mode == LanSessionMode.desktop) {
        onDesktopChanged?.call();
      }
    };
    meta.onMessage = (message) {
      final time = ClockSync.nowNanos();
      if (!message.isBinary ||
          message.binary.isEmpty ||
          _closed ||
          _ended ||
          _peer != peer) {
        return;
      }
      final bytes = message.binary;
      if (_passwordExchange != null) {
        if (mode == LanSessionMode.desktop && exchange!.accepted) {
          _receiveDesktop(bytes);
        }
        exchange!.receive(bytes);
        return;
      }
      if (bytes[0] == 0x62) {
        _end(_byeReason(bytes));
      } else if (decodeAccessibility(bytes) case final granted?) {
        _setAccessibilityDenied(!granted);
      } else if (mode == LanSessionMode.desktop &&
          (_hostKey != null || _pairAnswerVerified) &&
          _receiveDesktop(bytes)) {
        return;
      } else if (_measure && bytes[0] == 0x63 && bytes.length == 13) {
        final capture = ByteData.sublistView(bytes);
        _captures[capture.getUint32(1)] = capture.getInt64(5);
        if (_captures.length > 480) _captures.remove(_captures.keys.first);
      } else {
        _clock.receive(bytes, time);
      }
    };
    if (mode == LanSessionMode.desktop) {
      await peer.addTransceiver(
        kind: RTCRtpMediaType.RTCRtpMediaTypeVideo,
        init: RTCRtpTransceiverInit(direction: TransceiverDirection.RecvOnly),
      );
    }
    final offer = mode == LanSessionMode.files
        ? await peer.createOffer({
            'mandatory': {
              'OfferToReceiveAudio': false,
              'OfferToReceiveVideo': false,
            },
          })
        : await peer.createOffer();
    await peer.setLocalDescription(offer);
    if (peer.iceGatheringState !=
        RTCIceGatheringState.RTCIceGatheringStateComplete) {
      try {
        await gather.future.timeout(const Duration(seconds: 8));
      } on TimeoutException {
        // Gathering may never complete: on a phone the loopback and VPN
        // interfaces keep retrying STUN/TURN they cannot reach. Offer what
        // has been gathered (the relay and server-reflexive candidates are
        // usually ready within a second) instead of failing.
      }
    }
    return peer;
  }

  void _admitDesktop(String key) {
    if (_mode != LanSessionMode.desktop) return;
    _hostKey = key;
    for (final update in _initialDesktopUpdates.values) {
      _desktopStatus = _desktopStatus.apply(update);
    }
    _initialDesktopUpdates.clear();
    onDesktopChanged?.call();
  }

  bool _receiveDesktop(Uint8List bytes) {
    final update = DesktopMeta.decode(bytes);
    if (update == null) return false;
    if (_hostKey == null) {
      // Keep only the latest state per kind while authentication finishes.
      if (update is! UnlockReply) {
        final key = update is PrivacyReply ? update.kind : update.runtimeType;
        _initialDesktopUpdates
          ..remove(key)
          ..[key] = update;
      }
    } else if (update is UnlockReply) {
      onUnlockResult?.call(update.result);
    } else {
      if (update is CursorUpdate && update.shape == _desktopStatus.cursor) {
        return true;
      }
      _desktopStatus = _desktopStatus.apply(update);
      onDesktopChanged?.call();
    }
    return true;
  }

  void _end(String reason) {
    if (_ended || _closed || _disconnecting) return;
    _ended = true;
    onEnded(reason);
  }

  Future<bool> setPrivacyScreen(bool on) =>
      _sendDesktop(encodePrivacyRequest(on));

  Future<bool> setLockAfterDisconnect(bool on) =>
      _sendDesktop(encodeLockAfterDisconnectRequest(on));

  Future<bool> lockAndDisconnect() => _sendDesktop(encodeLockNowRequest());

  Future<bool> sendUnlock(Uint8List secret) async {
    if (!desktopReady || desktopStatus.lockScreen != LockScreenState.ready) {
      return false;
    }
    final frame = encodeUnlockRequest(secret);
    if (frame == null) return false;
    try {
      return await _sendDesktop(frame);
    } finally {
      frame.fillRange(1, frame.length, 0);
    }
  }

  Future<bool> _sendDesktop(Uint8List bytes) async {
    if (!desktopReady) return false;
    try {
      await _meta!.send(RTCDataChannelMessage.fromBinary(bytes));
      return true;
    } on Exception {
      return false;
    }
  }

  Future<void> disconnect() async {
    if (_disconnecting || _closed) return;
    final meta = desktopReady ? _meta : null;
    _disconnecting = true;
    try {
      if (meta != null) {
        await meta
            .send(RTCDataChannelMessage.fromBinary(Uint8List.fromList([0x62])))
            .timeout(const Duration(milliseconds: 150));
        // Give the empty bye time to arrive before tearing down WebRTC.
        await Future<void>.delayed(const Duration(milliseconds: 150));
      }
    } on Exception {
      // Close even if the data channel is already gone.
    } finally {
      await close();
    }
  }

  void _publishTrack(MediaStream stream, String? trackId) {
    onStream(stream);
    if (_measure && Platform.isIOS) unawaited(_startFrameProbe(trackId));
  }

  void _startClock(RTCDataChannel meta) {
    _ping?.cancel();
    unawaited(_sendBinary(meta, _clock.ping()));
    _ping = Timer.periodic(const Duration(seconds: 1), (_) {
      unawaited(_sendBinary(meta, _clock.ping()));
    });
  }

  Future<void> _startFrameProbe(String? trackId) async {
    try {
      await _probeChannel.invokeMethod<void>('startFrameProbe', trackId);
      if (_closed) {
        await _probeChannel.invokeMethod<void>('stopFrameProbe');
      } else {
        _frameProbe = true;
      }
    } catch (error) {
      if (!_closed) await _recordStats({'frameProbeError': '$error'});
    }
  }

  Future<void> _logStats(RTCPeerConnection peer) async {
    try {
      final reports = await peer.getStats();
      final video = reports
          .where((r) => r.type == 'inbound-rtp' && r.values['kind'] == 'video')
          .firstOrNull;
      if (video == null || _closed) return;
      final sync = _clock.best;
      final probe = _frameProbe
          ? await _probeChannel.invokeMapMethod<String, dynamic>(
              'readFrameProbe',
            )
          : null;
      final delays = <double>[];
      final frames = probe?['frames'] as List<dynamic>?;
      if (sync != null && frames != null) {
        for (final frame in frames.cast<List<dynamic>>()) {
          final capture = _captures.remove(frame[0] as int);
          if (capture != null) {
            delays.add(((frame[1] as int) - capture + sync.offset) / 1000000);
          }
        }
      }
      final codec = reports
          .where((r) => r.id == video.values['codecId'])
          .firstOrNull;
      await _recordStats({
        'timeNs': ClockSync.nowNanos(),
        'codec': codec?.values['mimeType'],
        for (final key in [
          'decoderImplementation',
          'powerEfficientDecoder',
          'framesDecoded',
          'framesDropped',
          'framesPerSecond',
          'frameWidth',
          'frameHeight',
          'packetsReceived',
          'packetsLost',
          'nackCount',
          'pliCount',
          'firCount',
          'retransmittedPacketsReceived',
          'jitter',
          'jitterBufferDelay',
          'jitterBufferTargetDelay',
          'jitterBufferMinimumDelay',
          'jitterBufferEmittedCount',
          'totalDecodeTime',
          'totalProcessingDelay',
        ])
          key: video.values[key],
        'clockRttMs': sync == null ? null : sync.rtt / 1000000,
        'clockOffsetNs': sync?.offset,
        if (probe != null) 'thermalState': probe['thermalState'],
        if (probe != null) 'sinkFrames': frames?.length,
        if (delays.isNotEmpty) 'captureToVideoSinkMs': delays,
      });
    } catch (error) {
      if (!_closed) await _recordStats({'statsError': '$error'});
    }
  }

  Future<void> _recordStats(Map<String, dynamic> data) async {
    final line = jsonEncode(data);
    debugPrint('kkdesk: $line');
    if (Platform.isIOS) {
      try {
        await _probeChannel.invokeMethod<void>('appendStats', line);
      } catch (error) {
        debugPrint('kkdesk: write stats: $error');
      }
    }
  }

  Future<Map<String, dynamic>> _receive(StreamIterator<String> messages) async {
    if (!await messages.moveNext().timeout(_exchangeTimeout)) {
      throw const SocketException('电脑关闭了连接');
    }
    return jsonDecode(messages.current) as Map<String, dynamic>;
  }

  void _send(Socket socket, Map<String, dynamic> value) {
    socket.writeln(jsonEncode(value));
  }

  Future<void> _sendBinary(RTCDataChannel? channel, Uint8List data) async {
    if (_closed ||
        _passwordExchange != null ||
        channel?.state != RTCDataChannelState.RTCDataChannelOpen) {
      return;
    }
    try {
      await channel!.send(RTCDataChannelMessage.fromBinary(data));
    } catch (error) {
      _end('数据通道已断开');
    }
  }

  void move(double x, double y) =>
      unawaited(_sendBinary(_move, mouseMove(x, y)));

  void button(double x, double y, bool down) =>
      unawaited(_sendBinary(_input, mouseButton(x, y, down)));

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _passwordExchange?.stop('连接已取消');
    _socket?.destroy();
    if (_negotiated case final pending? when !pending.isCompleted) {
      pending.complete(false);
    }
    await _pairTransport?.close();
    _ping?.cancel();
    _stats?.cancel();
    await _resetPeer();
    if (_frameProbe) await _probeChannel.invokeMethod<void>('stopFrameProbe');
  }
}

String _byeReason(Uint8List bytes) {
  final reason = utf8.decode(bytes.sublist(1), allowMalformed: true);
  return reason.isEmpty ? '电脑已结束连接' : reason;
}

class _LanPasswordExchange {
  static const _open = RTCDataChannelState.RTCDataChannelOpen;
  static const _closed = RTCDataChannelState.RTCDataChannelClosed;
  late final List<RTCDataChannel> channels;
  late final RTCDataChannel meta;
  late final RTCDataChannel files;
  final _reply = Completer<LanAuthResult?>();
  String? ended;
  RTCTrackEvent? track;
  bool? accessibility;
  bool accepted = false;

  Future<void> authenticate(Uint8List proof) async {
    try {
      await Future.wait(
        channels.map((channel) async {
          if (channel.state == _open) return;
          if (channel.state == _closed ||
              await channel.stateChangeStream.firstWhere(
                    (state) => state == _open || state == _closed,
                  ) !=
                  _open) {
            throw const LanAuthFailure('无法建立 WebRTC 数据通道');
          }
        }),
      ).timeout(_exchangeTimeout);
      if (ended case final reason?) throw LanAuthFailure(reason);
      await meta.send(RTCDataChannelMessage.fromBinary(proof));
      final result = await _reply.future.timeout(_exchangeTimeout);
      if (result == null) throw LanAuthFailure(ended ?? '电脑已结束连接');
      if (result.failure case final failure?) throw LanAuthFailure(failure);
    } on TimeoutException {
      throw const LanAuthFailure('密码验证超时，请两边都升级到最新版');
    }
  }

  void receive(Uint8List bytes) {
    if (bytes[0] == 0x62) {
      stop(_byeReason(bytes));
    } else if (LanAuthResult.decode(bytes) case final result?) {
      if (!_reply.isCompleted) {
        accepted = result.failure == null;
        _reply.complete(result);
      }
    } else if (decodeAccessibility(bytes) case final granted?) {
      accessibility = granted;
    }
  }

  void stop(String reason) {
    ended ??= reason;
    if (!_reply.isCompleted) _reply.complete(null);
  }
}

// webrtc_interface currently omits maxRetransmits when it is zero.
class _UnreliableChannel extends RTCDataChannelInit {
  @override
  Map<String, dynamic> toMap() => {
    ...super.toMap(),
    'ordered': false,
    'maxRetransmits': 0,
  };
}
