part of 'files_client.dart';

class _Upload extends _Job {
  _Upload(int id, this.source, this.remoteDirectory)
    : super(id, source.name, FileTransferDirection.upload);

  final UploadSource source;
  final String remoteDirectory;
  final _directories = <String>[];
  final _files = <({String rel, UploadSource source})>[];
  FileTransferError? _hostWriteError;
  String? _activeRel;

  @override
  Future<void> run(FilesClient client) async {
    if (!_validComponent(source.name)) {
      throw const FileTransferError('invalid', 'Invalid upload name');
    }
    totalBytes = 0;
    await _plan(source, source.name, client.requestTimeout);
    totalFiles = _files.length + failures.length;
    publish(client, force: true);
    if (source.isDirectory) {
      name = await _mkdir(client, source.name, unique: true);
      if (!_validComponent(name)) {
        throw const FileTransferError('invalid', 'Invalid remote folder name');
      }
      for (final directory in _directories.skip(1)) {
        try {
          await _mkdir(client, _rebase(directory));
        } catch (error) {
          final failure = _asFileError(error);
          if (_fatal(failure)) rethrow;
          failures[directory] = failure;
        }
      }
    }
    for (final file in _files) {
      check();
      currentName = file.source.name;
      publish(client, force: true);
      try {
        final actual = await _sendFile(client, file.source, _rebase(file.rel));
        files++;
        bytesSaved += file.source.size;
        if (!source.isDirectory) name = actual;
      } catch (error) {
        final failure = _asFileError(error);
        if (_fatal(failure)) rethrow;
        failures[file.rel] = failure;
      }
      filesDone++;
      publish(client, force: true);
    }
  }

  Future<void> _plan(UploadSource item, String rel, Duration timeout) async {
    check();
    if (!_validComponent(item.name)) {
      failures[rel] = const FileTransferError('invalid', 'Invalid upload name');
      return;
    }
    if (item.isLink) {
      skipped.add(rel);
      return;
    }
    if (item.error case final error?) {
      failures[rel] = _asFileError(error);
      return;
    }
    if (item.listChildren case final children?) {
      _directories.add(rel);
      List<UploadSource> entries;
      try {
        entries = await untilStopped(
          children().timeout(timeout, onTimeout: () => throw _timeoutError),
        );
      } catch (error) {
        final failure = _asFileError(error);
        if (_fatal(failure)) rethrow;
        failures[rel] = failure;
        return;
      }
      for (final child in entries) {
        await _plan(child, '$rel/${child.name}', timeout);
      }
    } else if (item.size >= 0) {
      _files.add((rel: rel, source: item));
      totalBytes = totalBytes! + item.size;
    } else {
      failures[rel] = const FileTransferError(
        'invalid',
        'Negative upload size',
      );
    }
  }

  String _rebase(String rel) =>
      source.isDirectory ? '$name${rel.substring(source.name.length)}' : rel;

  Future<String> _mkdir(
    FilesClient client,
    String rel, {
    bool unique = false,
  }) async {
    final response = await client._request(
      'mkdir',
      job: this,
      fields: {
        'transfer': id,
        'dir': remoteDirectory,
        'rel': rel,
        if (unique) 'unique': true,
      },
    );
    return response['name'] as String;
  }

  @override
  void notice(FilesClient client, FileMessage message) {
    if (message['op'] == 'file_error' &&
        message['rel'] == _activeRel &&
        _activeRel != null) {
      _hostWriteError = _wireError(message);
    }
  }

  Future<String> _sendFile(
    FilesClient client,
    UploadSource input,
    String rel,
  ) async {
    _activeRel = rel;
    _hostWriteError = null;
    final modified = input.modified?.microsecondsSinceEpoch ?? 0;
    final end = <String, dynamic>{
      'transfer': id,
      'rel': rel,
      if (input.size != 0) 'size': input.size,
    };
    await client._request(
      'upload_begin',
      job: this,
      fields: {
        'transfer': id,
        'dir': remoteDirectory,
        'rel': rel,
        if (input.size != 0) 'size': input.size,
        if (modified != 0) 'mtime': modified * 1000,
      },
    );
    FileTransferError? failure;
    try {
      end['sha256'] = await _writeChunks(client, input);
    } catch (error) {
      failure = _asFileError(error);
      if (_fatal(failure)) rethrow;
      end['error'] = failure.code;
      if (failure.detail.isNotEmpty) end['detail'] = failure.detail;
    }
    try {
      final response = await client._request('end', job: this, fields: end);
      if (failure != null) throw failure;
      if (_hostWriteError case final error?) throw error;
      return response['name'] as String;
    } finally {
      _activeRel = null;
    }
  }

  Future<String> _writeChunks(FilesClient client, UploadSource input) async {
    final chunks = StreamIterator(input.openRead!());
    final digest = _DigestSink();
    final hash = sha256.startChunkedConversion(digest);
    var offset = 0;
    try {
      while (offset < input.size &&
          await untilStopped(
            chunks.moveNext().timeout(
              client.requestTimeout,
              onTimeout: () => throw _timeoutError,
            ),
          )) {
        final block = chunks.current;
        final count = math.min(block.length, input.size - offset);
        for (var index = 0; index < count;) {
          if (_hostWriteError case final error?) throw error;
          await client._waitFlow(this);
          if (_hostWriteError case final error?) throw error;
          check();
          final next = math.min(index + fileChunkSize, count);
          final data = block.sublist(index, next);
          await untilStopped(
            client._channel
                .sendBinary(FileFrame(fileDataKind, id, offset, data).encode())
                .timeout(
                  client.requestTimeout,
                  onTimeout: () => throw _timeoutError,
                ),
          );
          hash.add(data);
          offset += data.length;
          transferred += data.length;
          publish(client);
          index = next;
        }
      }
    } finally {
      // A provider may finish cleaning up its stream asynchronously.
      unawaited(chunks.cancel().then<void>((_) {}, onError: (Object _) {}));
    }
    if (offset != input.size) {
      throw const FileTransferError(
        'checksum',
        'Source ended before its stated size',
      );
    }
    hash.close();
    return digest.value.toString();
  }

  bool _fatal(FileTransferError error) => const {
    'denied',
    'closed',
    'canceled',
    'version',
    'timeout',
  }.contains(error.code);
}
