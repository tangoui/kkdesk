import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'files/file_transfer_service.dart';
import 'files/lan_file_transfer_service.dart';
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
  LanSession({required this.onStream, required this.onEnded});

  final void Function(MediaStream stream) onStream;
  final void Function(String message) onEnded;
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

  bool get inputReady =>
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
    final endpoint = Uri.tryParse('tcp://$address');
    if (endpoint == null ||
        endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        endpoint.path.isNotEmpty ||
        endpoint.hasQuery ||
        endpoint.hasFragment ||
        (endpoint.hasPort && (endpoint.port < 1 || endpoint.port > 65535))) {
      throw const FormatException('请输入电脑地址，可附加端口');
    }

    try {
      final socket = await Socket.connect(
        endpoint.host,
        endpoint.hasPort ? endpoint.port : 21118,
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
        final nonce = await _receive(messages);
        _send(socket, {
          'mac': challengeMac(password, nonce['nonce'] as String),
        });
        final auth = await _receive(messages);
        if (auth['ok'] != true) throw StateError('电脑拒绝了连接密码');
        final peer = await _offer(mode, address);
        final local = await peer.getLocalDescription();
        _send(socket, {'sdp': local!.toMap(), 'mode': mode.name});
        final reply = await _receive(messages);
        final answer = reply['sdp'] as Map<String, dynamic>;
        if (answer['type'] != 'answer') {
          throw const FormatException('电脑返回了无效的应答');
        }
        await peer.setRemoteDescription(
          RTCSessionDescription(answer['sdp'] as String, 'answer'),
        );
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
    peer.onTrack = (event) {
      if (!_closed && event.track.kind == 'video' && event.streams.isNotEmpty) {
        onStream(event.streams.first);
        if (_measure && Platform.isIOS) {
          unawaited(_startFrameProbe(event.track.id));
        }
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
    final files = LanFileTransferService(filesChannel, address);
    if (_closed) {
      await files.close();
      throw StateError('连接已取消');
    }
    _files = files;
    meta.onDataChannelState = (state) {
      _ping?.cancel();
      if (state == RTCDataChannelState.RTCDataChannelOpen && !_closed) {
        unawaited(_sendBinary(meta, _clock.ping()));
        _ping = Timer.periodic(const Duration(seconds: 1), (_) {
          unawaited(_sendBinary(meta, _clock.ping()));
        });
      }
    };
    meta.onMessage = (message) {
      final time = ClockSync.nowNanos();
      if (!message.isBinary || message.binary.isEmpty || _closed) return;
      final bytes = message.binary;
      if (bytes[0] == 0x62) {
        final reason = utf8.decode(bytes.sublist(1), allowMalformed: true);
        onEnded(reason.isEmpty ? '电脑已结束连接' : reason);
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
      await gather.future.timeout(const Duration(seconds: 8));
    }
    return peer;
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
    if (_closed || channel?.state != RTCDataChannelState.RTCDataChannelOpen) {
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

// webrtc_interface currently omits maxRetransmits when it is zero.
class _UnreliableChannel extends RTCDataChannelInit {
  @override
  Map<String, dynamic> toMap() => {
    ...super.toMap(),
    'ordered': false,
    'maxRetransmits': 0,
  };
}
