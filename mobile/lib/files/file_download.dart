part of 'files_client.dart';

class _Download extends _Job {
  _Download(int id, this.remotePath, this.destination)
    : super(
        id,
        remotePath.split(RegExp(r'[/\\]')).last,
        FileTransferDirection.download,
      );

  final String remotePath;
  final Directory destination;
  final _directories = <String, Directory>{};
  final _done = Completer<FileTransferError?>();
  Timer? _idle;
  _IncomingFile? _incoming;
  String? _rel;
  String? _remoteName;

  @override
  Future<void> run(FilesClient client) async {
    if (!await untilStopped(destination.exists())) {
      throw FileTransferError('not_found', destination.path);
    }
    await client._request(
      'download',
      path: remotePath,
      fields: {'transfer': id},
      job: this,
      accept: (reply) {
        _remoteName = name = reply['name'] as String;
        totalBytes = reply['size'] as int? ?? 0;
        totalFiles = reply['files'] as int? ?? 0;
        skipped.addAll((reply['skipped'] as List? ?? const []).cast<String>());
        _resetIdle(client);
        publish(client, force: true);
      },
    );
    if (await untilStopped(_done.future) case final failure?) throw failure;
  }

  void _resetIdle(FilesClient client) {
    _idle?.cancel();
    _idle = Timer(
      client.requestTimeout,
      () => client._stop(this, _timeoutError),
    );
  }

  @override
  void interrupt() {
    _idle?.cancel();
    _discard();
  }

  void _discard() {
    _incoming?.discard();
    _incoming = null;
  }

  @override
  void notice(FilesClient client, FileMessage message) {
    if (_remoteName == null || _done.isCompleted) return;
    _resetIdle(client);
    final rel = message['rel'] as String? ?? '';
    final op = message['op'];
    try {
      switch (op) {
        case 'dir':
          final path = _downloadPath(rel);
          final candidate = path.parent;
          final folder = path.isRoot
              ? _availableName(candidate, path.name)
              : path.name;
          final directory = Directory(_join(candidate, folder));
          // The root is always fresh. Every child must either be fresh or
          // already registered by an earlier directory notice.
          if (!_directories.containsKey(rel)) {
            if (_exists(directory.path)) throw FileTransferError('exists', rel);
            directory.createSync();
            _directories[rel] = directory;
          }
          if (path.isRoot) name = folder;
        case 'file':
          _failIncomplete();
          _rel = rel;
          final path = _downloadPath(rel);
          _incoming = _IncomingFile(
            path.parent,
            path.name,
            message['size'] as int? ?? 0,
            message['mtime'] as int? ?? 0,
          );
          currentName = path.name;
          publish(client, force: true);
        case 'end':
          _endFile(message, rel);
          publish(client, force: true);
        case 'skip':
          failures[rel] = _wireError(message);
          filesDone++;
          publish(client, force: true);
        case 'done':
          _failIncomplete();
          for (final row in message['failed'] as List? ?? const []) {
            final failed = row as FileMessage;
            failures.putIfAbsent(
              failed['rel'] as String,
              () => _wireError(failed),
            );
          }
          if (_directories.isEmpty && files == 0 && failures.isEmpty) {
            throw const FileTransferError(
              'invalid',
              'No file or directory received',
            );
          }
          _idle?.cancel();
          _done.complete(null);
      }
    } on Object catch (error) {
      final failure = _asFileError(error);
      if (op == 'done') {
        if (!_done.isCompleted) _done.complete(failure);
      } else {
        failures[rel] = failure;
      }
    }
  }

  ({Directory parent, String name, bool isRoot}) _downloadPath(String rel) {
    final parts = rel.split('/');
    if (parts.any((part) => !_validComponent(part)) ||
        parts.first != _remoteName) {
      throw FileTransferError('invalid', 'Invalid relative path: $rel');
    }
    var name = parts.last.replaceAll('\u0000', '_');
    if (Platform.isWindows) {
      name = name.replaceAll(RegExp(r'[<>:"\\|?*\x00-\x1f]'), '_');
      name = name.replaceFirstMapped(
        RegExp(r'[. ]+$'),
        (m) => '_' * m[0]!.length,
      );
      if (RegExp(
        r'^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)',
        caseSensitive: false,
      ).hasMatch(name)) {
        name = '_$name';
      }
    }
    final isRoot = parts.length == 1;
    final parent = isRoot
        ? destination
        : _directories[parts.take(parts.length - 1).join('/')];
    if (parent == null ||
        (!isRoot &&
            FileSystemEntity.typeSync(parent.path, followLinks: false) !=
                FileSystemEntityType.directory)) {
      throw FileTransferError('invalid', 'Missing parent directory: $rel');
    }
    return (parent: parent, name: name, isRoot: isRoot);
  }

  void receiveBytes(FilesClient client, FileFrame frame) {
    final rel = _rel;
    if (rel == null || _done.isCompleted) return;
    _resetIdle(client);
    try {
      _incoming?.write(frame.offset, frame.payload);
    } catch (error) {
      _discard();
      failures[rel] = _asFileError(error);
    }
    transferred += frame.payload.length;
    publish(client);
  }

  void _failIncomplete() {
    if (_rel != null) {
      failures.putIfAbsent(
        _rel!,
        () => const FileTransferError('checksum', 'Missing file end'),
      );
      _rel = null;
      _discard();
    }
  }

  void _endFile(FileMessage message, String rel) {
    final current = _rel;
    if (current == null) return;
    final incoming = _incoming;
    try {
      if (incoming == null) return;
      if (rel.isNotEmpty && rel != current) {
        throw const FileTransferError('invalid', 'Unexpected file end');
      }
      if (message['error'] != null) throw _wireError(message);
      final actual = incoming.finish(message['sha256'] as String? ?? '');
      files++;
      bytesSaved += incoming.size;
      if (!current.contains('/')) name = actual;
    } catch (error) {
      failures[current] = _asFileError(error);
    } finally {
      _discard();
      _rel = null;
      filesDone++;
    }
  }
}

class _IncomingFile {
  _IncomingFile(this.directory, this.name, this.size, this.mtime) {
    if (size < 0) {
      throw const FileTransferError('invalid', 'Negative file size');
    }
    final tempName = _availableName(directory, '.$name.kkdesk-part');
    _temp = File(_join(directory, tempName));
    _temp.createSync(exclusive: true);
    try {
      _file = _temp.openSync(mode: FileMode.writeOnly);
    } catch (_) {
      discard();
      rethrow;
    }
  }

  final Directory directory;
  final String name;
  final int size;
  final int mtime;
  late final File _temp;
  RandomAccessFile? _file;
  final _digest = _DigestSink();
  late final _hash = sha256.startChunkedConversion(_digest);
  int _written = 0;
  bool _finished = false;

  void write(int offset, List<int> bytes) {
    if (offset != _written ||
        bytes.length > fileChunkSize ||
        bytes.length > size - _written) {
      throw const FileTransferError(
        'checksum',
        'Unexpected chunk offset or size',
      );
    }
    _file!.writeFromSync(bytes);
    _hash.add(bytes);
    _written += bytes.length;
  }

  String finish(String expectedSha256) {
    _hash.close();
    if (_written != size || _digest.value.toString() != expectedSha256) {
      throw const FileTransferError('checksum', 'Size or SHA-256 mismatch');
    }
    final file = _file!;
    _file = null;
    try {
      file.flushSync();
    } finally {
      file.closeSync();
    }
    final reserved = _reserveDownloadName(directory, name);
    File saved;
    try {
      saved = _temp.renameSync(reserved.path);
    } catch (_) {
      reserved.deleteSync();
      rethrow;
    }
    _finished = true;
    if (mtime != 0) {
      try {
        saved.setLastModifiedSync(
          DateTime.fromMicrosecondsSinceEpoch(mtime ~/ 1000),
        );
      } on FileSystemException {
        // Best effort, as in the desktop engine.
      }
    }
    return fileBaseName(saved.path);
  }

  void discard() {
    try {
      _file?.closeSync();
    } on FileSystemException {
      // Still try to remove the temporary file.
    }
    _file = null;
    if (_finished) return;
    try {
      _temp.deleteSync();
    } on FileSystemException {
      // The directory may already have been removed or become read-only.
    }
  }
}

bool _validComponent(String name) =>
    name.isNotEmpty && name != '.' && name != '..' && !name.contains('/');

String _join(Directory parent, String name) =>
    '${parent.path}${Platform.pathSeparator}$name';

bool _exists(String path) =>
    FileSystemEntity.typeSync(path, followLinks: false) !=
    FileSystemEntityType.notFound;

String _availableName(Directory parent, String name) {
  final dot = name.lastIndexOf('.');
  final stem = dot <= 0 ? name : name.substring(0, dot);
  final extension = dot <= 0 ? '' : name.substring(dot);
  var candidate = name;
  for (var i = 1; _exists(_join(parent, candidate)); i++) {
    candidate = '$stem ($i)$extension';
  }
  return candidate;
}

File _reserveDownloadName(Directory parent, String name) {
  while (true) {
    final file = File(_join(parent, _availableName(parent, name)));
    try {
      file.createSync(exclusive: true);
      return file;
    } on FileSystemException {
      // Another transfer may have claimed the name after the lookup.
      if (!_exists(file.path)) rethrow;
    }
  }
}
