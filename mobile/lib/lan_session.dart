import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

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
  });

  final void Function(MediaStream stream) onStream;
  final void Function(String message) onEnded;
  final VoidCallback? onAccessibilityChanged;
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
        socket
            .cast<List<int>>()
            .transform(utf8.decoder)
            .transform(const LineSplitter()),
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
        if (exchange.ended case final reason?) throw LanAuthFailure(reason);
        _files = LanFileTransferService(exchange.files, address);
        _passwordExchange = null;
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
        await peer.setRemoteDescription(
          RTCSessionDescription(reply['sdp'] as String, 'answer'),
        );
        if (!await ready.future.timeout(const Duration(seconds: 10))) {
          throw const SocketException('无法建立 WebRTC 连接');
        }
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
    _move = _input = null;
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
          } else if (!_closed) {
            unawaited(_files?.close());
            onEnded('远程连接已断开');
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
      _ping?.cancel();
      if (state == RTCDataChannelState.RTCDataChannelOpen &&
          _passwordExchange == null &&
          !_closed) {
        _startClock(meta);
      }
    };
    meta.onMessage = (message) {
      final time = ClockSync.nowNanos();
      if (!message.isBinary ||
          message.binary.isEmpty ||
          _closed ||
          _peer != peer) {
        return;
      }
      final bytes = message.binary;
      if (_passwordExchange != null) {
        exchange!.receive(bytes);
        return;
      }
      if (bytes[0] == 0x62) {
        onEnded(_byeReason(bytes));
      } else if (decodeAccessibility(bytes) case final granted?) {
        _setAccessibilityDenied(!granted);
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
      if (!_closed) onEnded('数据通道已断开');
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
      if (!_reply.isCompleted) _reply.complete(result);
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
