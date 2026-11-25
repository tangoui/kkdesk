import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'file_protocol.dart';
import 'file_transfer_service.dart';
import 'files_client.dart';

/// The service for the ordered, reliable files channel of a LanSession.
class LanFileTransferService extends FilesClient {
  factory LanFileTransferService(RTCDataChannel channel, String address) =>
      LanFileTransferService._(_RtcFileChannel(channel), address);

  LanFileTransferService._(this._rtc, String address)
    : _historyKey = 'files.history.$address',
      super(_rtc) {
    _loaded = _loadHistory();
  }

  final _RtcFileChannel _rtc;
  final String _historyKey;
  final _preferences = SharedPreferencesAsync();
  late final Future<void> _loaded;
  Future<void> _saving = Future.value();

  Future<LanFileTransferService> get ready async {
    await _rtc.waitUntilOpen();
    await _loaded;
    return this;
  }

  @override
  Future<void> close() async {
    _rtc.detach();
    await super.close();
  }

  Future<void> _loadHistory() async {
    try {
      final saved = await _preferences.getStringList(_historyKey) ?? const [];
      restoreHistory(saved.take(100).map(_decodeResult).nonNulls);
    } catch (_) {
      // Transfers are available even when the optional history store fails.
    }
  }

  @override
  void didFinish(FileTransferResult result) {
    _saving = _saving
        .then((_) async {
          await _loaded;
          await _preferences.setStringList(_historyKey, [
            for (final result in history) _encodeResult(result),
          ]);
        })
        .catchError((Object _) {});
  }
}

class _RtcFileChannel implements FileChannel {
  _RtcFileChannel(this._channel) {
    _channel.onMessage = (message) {
      if (_messages.isClosed) return;
      _messages.add(
        message.isBinary
            ? FileChannelMessage.binary(message.binary)
            : FileChannelMessage.text(message.text),
      );
    };
    _channel.onBufferedAmountLow = (_) {
      if (!_low.isClosed) _low.add(null);
    };
    _channel.onDataChannelState = (state) {
      if (state == RTCDataChannelState.RTCDataChannelOpen &&
          !_opened.isCompleted) {
        _opened.complete();
      } else if (state == RTCDataChannelState.RTCDataChannelClosed) {
        detach();
      }
    };
    final state = _channel.state;
    if (state == RTCDataChannelState.RTCDataChannelOpen) {
      _opened.complete();
    } else if (state == RTCDataChannelState.RTCDataChannelClosed) {
      detach();
    }
  }

  final RTCDataChannel _channel;
  final _messages = StreamController<FileChannelMessage>(sync: true);
  final _low = StreamController<void>.broadcast(sync: true);
  final _opened = Completer<void>();

  Future<void> waitUntilOpen() async {
    await _opened.future.timeout(
      const Duration(seconds: 15),
      onTimeout: () => throw const FileTransferError('timeout'),
    );
    if (_messages.isClosed ||
        _channel.state != RTCDataChannelState.RTCDataChannelOpen) {
      throw const FileTransferError('closed');
    }
  }

  void detach() {
    if (_messages.isClosed) return;
    _channel.onMessage = null;
    _channel.onBufferedAmountLow = null;
    _channel.onDataChannelState = null;
    if (!_opened.isCompleted) _opened.complete();
    unawaited(_messages.close());
    unawaited(_low.close());
  }

  @override
  Stream<FileChannelMessage> get messages => _messages.stream;
  @override
  Stream<void> get bufferedAmountLow => _low.stream;
  @override
  set bufferedAmountLowThreshold(int bytes) =>
      _channel.bufferedAmountLowThreshold = bytes;

  @override
  Future<int> bufferedAmount() => _channel.getBufferedAmount();
  @override
  Future<void> sendText(String text) =>
      _channel.send(RTCDataChannelMessage(text));
  @override
  Future<void> sendBinary(Uint8List bytes) =>
      _channel.send(RTCDataChannelMessage.fromBinary(bytes));
}

String _encodeResult(FileTransferResult result) => jsonEncode({
  'id': result.id,
  'direction': result.direction.name,
  'name': result.name,
  'status': result.status.name,
  'bytes': result.bytes,
  'files': result.files,
  'finishedAt': result.finishedAt.toIso8601String(),
  'elapsedUs': result.elapsed.inMicroseconds,
  'skipped': result.skipped,
  'failures': [
    for (final failure in result.failures)
      [failure.path, failure.error.code, failure.error.detail],
  ],
  if (result.error case final error?) 'error': [error.code, error.detail],
});

FileTransferResult? _decodeResult(String text) {
  try {
    final data = jsonDecode(text) as FileMessage;
    final error = data['error'] as List?;
    return FileTransferResult(
      id: data['id'] as int,
      direction: FileTransferDirection.values.byName(
        data['direction'] as String,
      ),
      name: data['name'] as String,
      status: FileTransferStatus.values.byName(data['status'] as String),
      bytes: data['bytes'] as int,
      files: data['files'] as int,
      finishedAt: DateTime.parse(data['finishedAt'] as String),
      elapsed: Duration(microseconds: data['elapsedUs'] as int),
      skipped: (data['skipped'] as List).cast<String>(),
      failures: [
        for (final failure in data['failures'] as List)
          (
            path: failure[0] as String,
            error: FileTransferError(
              failure[1] as String,
              failure[2] as String,
            ),
          ),
      ],
      error: error == null
          ? null
          : FileTransferError(error[0] as String, error[1] as String),
    );
  } catch (_) {
    return null;
  }
}
