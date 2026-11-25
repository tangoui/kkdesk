import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:crypto/crypto.dart';

import 'file_protocol.dart';
import 'file_transfer_service.dart';

part 'file_download.dart';
part 'file_upload.dart';

const _closedError = FileTransferError('closed');
const _canceledError = FileTransferError('canceled');
const _timeoutError = FileTransferError('timeout');

class FilesClient implements FileTransferService {
  FilesClient(
    this._channel, {
    this.requestTimeout = const Duration(seconds: 60),
  }) {
    _channel.bufferedAmountLowThreshold = fileLowWater;
    _low = _channel.bufferedAmountLow.listen((_) {
      _flowWake?.complete();
      _flowWake = null;
    });
    _messages = _channel.messages.listen(
      _receive,
      onError: (Object _) => _disconnect(),
      onDone: _disconnect,
    );
  }

  final FileChannel _channel;
  final Duration requestTimeout;
  late final StreamSubscription<FileChannelMessage> _messages;
  late final StreamSubscription<void> _low;
  final _pending = <int, _Request>{};
  final _jobs = <int, _Job>{};
  final _history = <FileTransferResult>[];
  final _queueChanges =
      StreamController<List<FileTransferSnapshot>>.broadcast();
  final _fragments = BytesBuilder(copy: false);
  Completer<void>? _flowWake;
  Future<void> _textTail = Future.value();
  Future<void>? _hello;
  _Job? _active;
  bool _closed = false;
  int _nextId = 0;

  @override
  List<FileTransferSnapshot> get queue =>
      List.unmodifiable(_jobs.values.map((job) => job.snapshot()));
  @override
  Stream<List<FileTransferSnapshot>> get queueChanges => _queueChanges.stream;
  @override
  List<FileTransferResult> get history => List.unmodifiable(_history);

  @override
  Future<RemoteHome> home() async =>
      RemoteHome.fromMessage(_object(await _request('home'), 'home'));

  @override
  Future<RemoteListing> list(String path) async => RemoteListing.fromMessage(
    _object(await _request('list', path: path), 'listing'),
  );

  @override
  Future<RemoteEntry> stat(String path) async => RemoteEntry.fromMessage(
    _object(await _request('stat', path: path), 'entry'),
  );

  @override
  Future<void> mkdir(String path) async => _request('mkdir', path: path);

  @override
  FileTransferTask download(String remotePath, Directory localDirectory) =>
      _enqueue(_Download(++_nextId, remotePath, localDirectory));

  @override
  FileTransferTask upload(UploadSource source, String remoteDirectory) =>
      _enqueue(_Upload(++_nextId, source, remoteDirectory));

  @override
  void cancel(int id) {
    final job = _jobs[id];
    if (job != null) _stop(job, _canceledError);
  }

  void _stop(_Job job, FileTransferError error) {
    if (job.stop != null) return;
    job.stop = error;
    for (final interrupt in job.interruptions) {
      interrupt(error);
    }
    job.interruptions.clear();
    job.interrupt();
    if (job.startedOnHost && !_closed) {
      unawaited(
        _sendJson({'id': ++_nextId, 'op': 'cancel', 'transfer': job.id})
            .then<void>((_) {}, onError: (Object _) {}),
      );
    }
    if (_active != job) _finish(job, error);
  }

  Future<void> close() async {
    _disconnect();
    await _messages.cancel();
    await _low.cancel();
    unawaited(_queueChanges.close());
  }

  void _disconnect() {
    if (_closed) return;
    _closed = true;
    for (final job in _jobs.values.toList()) {
      _stop(job, _closedError);
    }
    for (final request in _pending.values.toList()) {
      if (!request.reply.isCompleted) request.reply.completeError(_closedError);
    }
  }

  FileTransferTask _enqueue(_Job job) {
    _jobs[job.id] = job;
    if (_closed) {
      _stop(job, _closedError);
    } else {
      _publish();
      _startNext();
    }
    return FileTransferTask(job.id, job.result.future);
  }

  void _startNext() {
    if (_active != null || _closed || _jobs.isEmpty) return;
    final job = _jobs.values.first;
    _active = job;
    job.clock.start();
    _publish();
    unawaited(_run(job));
  }

  Future<void> _run(_Job job) async {
    FileTransferError? error;
    try {
      await job.run(this);
    } catch (failure) {
      error = job.stop ?? _asFileError(failure);
      if (error.code == 'timeout') _stop(job, error);
    } finally {
      job.interrupt();
      _finish(job, job.stop ?? error);
      _active = null;
      _startNext();
    }
  }

  void _finish(_Job job, FileTransferError? error) {
    job.clock.stop();
    final status = switch ((error?.code, job.failures.isEmpty, job.files)) {
      ('canceled', _, _) => FileTransferStatus.canceled,
      (final String _, _, _) => FileTransferStatus.failed,
      (_, true, _) => FileTransferStatus.completed,
      (_, false, 0) => FileTransferStatus.failed,
      _ => FileTransferStatus.partial,
    };
    final result = FileTransferResult(
      id: job.id,
      name: job.name,
      direction: job.direction,
      status: status,
      bytes: job.bytesSaved,
      files: job.files,
      finishedAt: DateTime.now(),
      elapsed: job.clock.elapsed,
      skipped: job.skipped,
      failures: job.failures.entries.map((e) => (path: e.key, error: e.value)),
      error:
          error ??
          (status == FileTransferStatus.failed
              ? job.failures.values.firstOrNull
              : null),
    );
    _jobs.remove(job.id);
    _history.insert(0, result);
    if (_history.length > 100) _history.removeLast();
    didFinish(result);
    _publish();
    job.result.complete(result);
  }

  /// Used by the platform adapter to restore and save recent results.
  void restoreHistory(Iterable<FileTransferResult> results) {
    _history.addAll(results.take(math.max(0, 100 - _history.length)));
  }

  void didFinish(FileTransferResult result) {}

  void _publish() {
    if (!_queueChanges.isClosed) _queueChanges.add(queue);
  }

  Future<void> _negotiate() async {
    final reply = await _call('hello', {'version': fileProtocolVersion});
    if (reply['version'] != fileProtocolVersion) {
      throw FileTransferError(
        'version',
        'Host protocol version ${reply['version']}',
      );
    }
  }

  Future<FileMessage> _request(
    String op, {
    String? path,
    FileMessage fields = const {},
    _Job? job,
    void Function(FileMessage)? accept,
  }) async {
    final hello = _hello ??= _negotiate();
    try {
      await (job == null ? hello : job.untilStopped(hello));
    } catch (_) {
      if (job?.stop == null && identical(_hello, hello)) _hello = null;
      rethrow;
    }
    return _call(
      op,
      {if (path != null && path.isNotEmpty) 'path': path, ...fields},
      job: job,
      accept: accept,
    );
  }

  Future<FileMessage> _call(
    String op,
    FileMessage fields, {
    _Job? job,
    void Function(FileMessage)? accept,
  }) async {
    if (_closed) throw _closedError;
    job?.check();
    final id = ++_nextId;
    final request = _Request(accept);
    _pending[id] = request;
    try {
      final sending = _sendJson({'id': id, 'op': op, ...fields}, job: job);
      final reply = Future.wait<dynamic>([
        request.reply.future,
        sending,
      ], eagerError: true).then((values) => values.first as FileMessage);
      final timed = reply.timeout(
        requestTimeout,
        onTimeout: () => throw _timeoutError,
      );
      return await (job == null ? timed : job.untilStopped(timed));
    } finally {
      _pending.remove(id);
    }
  }

  Future<void> _sendJson(FileMessage message, {_Job? job}) {
    final sent = _textTail.then((_) async {
      if (_closed) throw _closedError;
      job?.check();
      if (job != null) job.startedOnHost = true;
      final text = encodeFileMessage(message);
      final bytes = utf8.encode(text);
      if (bytes.length > fileMaxJson) {
        throw const FileTransferError('invalid', 'Message too large');
      }
      var offset = 0;
      while (bytes.length - offset > fileMaxText) {
        var end = math.min(offset + fileChunkSize, bytes.length - 1);
        // A data channel text message must start on a Unicode boundary.
        while (end < bytes.length && (bytes[end] & 0xc0) == 0x80) {
          end--;
        }
        await _channel.sendBinary(
          FileFrame(fileJsonKind, 0, 0, bytes.sublist(offset, end)).encode(),
        );
        offset = end;
      }
      await _channel.sendText(
        offset == 0 ? text : utf8.decode(bytes.sublist(offset)),
      );
    });
    _textTail = sent.then<void>((_) {}, onError: (Object _) {});
    return sent;
  }

  void _receive(FileChannelMessage message) {
    if (_closed) return;
    try {
      final text = message.text;
      if (text != null) {
        final bytes = utf8.encode(text);
        final size = _fragments.length + bytes.length;
        _fragments.add(bytes);
        final json = _fragments.takeBytes();
        if (size <= fileMaxJson) _receiveJson(decodeFileMessage(json));
        return;
      }
      final frame = FileFrame.decode(message.binary!);
      switch (frame.kind) {
        case fileJsonKind:
          if (_fragments.length + frame.payload.length > fileMaxJson) {
            _fragments.clear();
          } else {
            _fragments.add(frame.payload);
          }
        case fileDataKind:
          final job = _active;
          if (job is _Download &&
              frame.transfer == job.id &&
              job.stop == null) {
            job.receiveBytes(this, frame);
          }
      }
    } on FormatException {
      _fragments.clear();
    }
  }

  void _receiveJson(FileMessage message) {
    if (message['ok'] is bool) {
      final request = _pending[message['id']];
      if (request == null || request.reply.isCompleted) return;
      if (message['ok'] == false) {
        request.reply.completeError(_wireError(message));
      } else {
        try {
          request.accept?.call(message);
          request.reply.complete(message);
        } catch (error) {
          request.reply.completeError(_asFileError(error));
        }
      }
      return;
    }
    final job = _active;
    if (job == null || job.id != message['transfer'] || job.stop != null) {
      return;
    }
    if (message['op'] == 'abort') {
      _stop(job, _wireError(message));
      return;
    }
    try {
      job.notice(this, message);
    } catch (error) {
      _stop(job, FileTransferError('invalid', error.toString()));
    }
  }

  Future<void> _waitFlow(_Job job) async {
    final wait = Stopwatch()..start();
    while (true) {
      job.check();
      final wake = _flowWake ??= Completer<void>();
      if (await job.untilStopped(
            _channel.bufferedAmount().timeout(
              requestTimeout,
              onTimeout: () => throw _timeoutError,
            ),
          ) <=
          fileHighWater) {
        return;
      }
      if (wait.elapsed >= requestTimeout) throw _timeoutError;
      final poll = Timer(const Duration(milliseconds: 50), () {
        if (identical(_flowWake, wake)) {
          _flowWake = null;
          wake.complete();
        }
      });
      try {
        await job.untilStopped(wake.future);
      } finally {
        poll.cancel();
      }
    }
  }
}

class _Request {
  _Request(this.accept);
  final void Function(FileMessage)? accept;
  final reply = Completer<FileMessage>();
}

abstract class _Job {
  _Job(this.id, this.name, this.direction);

  final int id;
  final FileTransferDirection direction;
  final result = Completer<FileTransferResult>();
  final interruptions = <void Function(FileTransferError)>{};
  final clock = Stopwatch();
  final skipped = <String>[];
  final failures = <String, FileTransferError>{};
  FileTransferError? stop;
  bool startedOnHost = false;
  String name;
  String? currentName;
  int? totalBytes;
  int? totalFiles;
  int transferred = 0;
  int bytesSaved = 0;
  int files = 0;
  int filesDone = 0;
  int _lastPublish = 0;

  Future<void> run(FilesClient client);
  void notice(FilesClient client, FileMessage message) {}
  void interrupt() {}

  void check() {
    final error = stop;
    if (error != null) throw error;
  }

  Future<T> untilStopped<T>(Future<T> operation) async {
    final outcome = Completer<T>();
    void interrupt(FileTransferError error) {
      if (!outcome.isCompleted) outcome.completeError(error);
    }

    // Attach handlers even if already canceled so late I/O errors are consumed.
    unawaited(
      operation.then<void>(
        (value) {
          if (!outcome.isCompleted) outcome.complete(value);
        },
        onError: (Object error, StackTrace stack) {
          if (!outcome.isCompleted) outcome.completeError(error, stack);
        },
      ),
    );

    if (stop case final error?) {
      interrupt(error);
    } else {
      interruptions.add(interrupt);
    }
    try {
      return await outcome.future;
    } finally {
      interruptions.remove(interrupt);
    }
  }

  void publish(FilesClient client, {bool force = false}) {
    final now = clock.elapsedMilliseconds;
    if (force || now - _lastPublish >= 100) {
      _lastPublish = now;
      client._publish();
    }
  }

  FileTransferSnapshot snapshot() => FileTransferSnapshot(
    id: id,
    direction: direction,
    name: currentName ?? name,
    status: clock.isRunning
        ? FileTransferStatus.running
        : FileTransferStatus.queued,
    bytes: transferred,
    totalBytes: totalBytes,
    filesDone: filesDone,
    totalFiles: totalFiles,
    bytesPerSecond: clock.elapsedMicroseconds == 0
        ? 0
        : transferred * 1e6 / clock.elapsedMicroseconds,
  );
}

FileMessage _object(FileMessage from, String key) {
  final object = from[key];
  if (object is FileMessage) return object;
  throw FileTransferError('invalid', 'Missing $key');
}

FileTransferError _wireError(FileMessage message) => FileTransferError(
  message['error'] is String ? message['error'] as String : 'io',
  message['detail'] is String ? message['detail'] as String : '',
);

FileTransferError _asFileError(Object error) => switch (error) {
  FileTransferError() => error,
  FormatException() => FileTransferError('invalid', error.message),
  FileSystemException() => FileTransferError('io', error.toString()),
  _ => FileTransferError('io', error.toString()),
};

class _DigestSink implements Sink<Digest> {
  late Digest value;

  @override
  void add(Digest data) => value = data;

  @override
  void close() {}
}
