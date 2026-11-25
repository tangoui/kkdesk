import 'dart:io';

import 'file_protocol.dart';

abstract interface class FileTransferService {
  Future<RemoteHome> home();
  Future<RemoteListing> list(String path);
  Future<RemoteEntry> stat(String path);
  Future<void> mkdir(String path);

  FileTransferTask download(String remotePath, Directory localDirectory);
  FileTransferTask upload(UploadSource source, String remoteDirectory);
  void cancel(int id);

  List<FileTransferSnapshot> get queue;
  Stream<List<FileTransferSnapshot>> get queueChanges;
  List<FileTransferResult> get history;
}

class RemoteHome {
  RemoteHome.fromMessage(FileMessage m)
    : directory = m['dir'] as String,
      volumes = List<String>.unmodifiable(m['volumes'] as List? ?? const []),
      separator = m['sep'] as String,
      operatingSystem = m['os'] as String;

  final String directory;
  final List<String> volumes;
  final String separator;
  final String operatingSystem;
}

class RemoteListing {
  RemoteListing.fromMessage(FileMessage m)
    : path = m['path'] as String,
      parent = m['parent'] as String? ?? '',
      truncated = m['truncated'] == true,
      entries = List.unmodifiable([
        for (final entry in m['entries'] as List? ?? const [])
          RemoteEntry.fromMessage(entry as FileMessage),
      ]);

  final String path;
  final String parent;
  final bool truncated;
  final List<RemoteEntry> entries;
}

class RemoteEntry {
  RemoteEntry.fromMessage(FileMessage m)
    : name = m['name'] as String,
      path = m['path'] as String,
      isDirectory = m['is_dir'] == true,
      size = m['size'] as int? ?? 0,
      modified = DateTime.parse(m['mtime'] as String),
      hidden = m['hidden'] == true,
      isLink = m['link'] == true;

  final String name;
  final String path;
  final bool isDirectory;
  final int size;
  final DateTime modified;
  final bool hidden;
  final bool isLink;
}

class UploadSource {
  /// Use a sandbox file, an exported photo, or a materialized document.
  static Future<UploadSource> fromPath(String path) async {
    final unchanged = File(path).absolute.path;
    final absolute = unchanged.replaceFirst(RegExp(r'(?<=.)/+$'), '');
    final name = fileBaseName(absolute);
    final stat = await FileStat.stat(absolute);
    switch (stat.type) {
      case FileSystemEntityType.directory:
        return UploadSource.directory(
          name: name,
          listChildren: () async {
            final children = <UploadSource>[];
            await for (final child in Directory(
              absolute,
            ).list(followLinks: false)) {
              try {
                children.add(
                  child is Link
                      ? UploadSource._unavailable(
                          fileBaseName(child.path),
                          isLink: true,
                        )
                      : await fromPath(child.path),
                );
              } on FileSystemException catch (error) {
                children.add(
                  UploadSource._unavailable(
                    fileBaseName(child.path),
                    error: error,
                  ),
                );
              }
            }
            children.sort((a, b) => a.name.compareTo(b.name));
            return children;
          },
        );
      case FileSystemEntityType.file:
        return UploadSource.file(
          name: name,
          size: stat.size,
          modified: stat.modified,
          openRead: File(absolute).openRead,
        );
      default:
        throw FileSystemException('Cannot read file or directory', absolute);
    }
  }

  /// A picker can supply readStream/XFile.openRead or read a document URI
  /// using its platform adapter. The stream is opened only when sending.
  const UploadSource.file({
    required this.name,
    required this.size,
    required Stream<List<int>> Function() this.openRead,
    this.modified,
  }) : listChildren = null,
       isLink = false,
       error = null;

  /// A picked document tree can provide children lazily.
  const UploadSource.directory({
    required this.name,
    required Future<List<UploadSource>> Function() this.listChildren,
  }) : size = 0,
       modified = null,
       openRead = null,
       isLink = false,
       error = null;

  const UploadSource._unavailable(this.name, {this.isLink = false, this.error})
    : size = 0,
      modified = null,
      openRead = null,
      listChildren = null;

  final String name;
  final int size;
  final DateTime? modified;
  final Stream<List<int>> Function()? openRead;
  final Future<List<UploadSource>> Function()? listChildren;
  final bool isLink;
  final FileSystemException? error;
  bool get isDirectory => listChildren != null;
}

String fileBaseName(String path) {
  final index = path.lastIndexOf(Platform.pathSeparator);
  return path.substring(index + 1);
}

class FileTransferError implements Exception {
  const FileTransferError(this.code, [this.detail = '']);

  final String code;
  final String detail;

  @override
  String toString() => detail.isEmpty ? code : '$code: $detail';
}

typedef FileTransferFailure = ({String path, FileTransferError error});

enum FileTransferDirection { upload, download }

enum FileTransferStatus {
  queued,
  running,
  completed,
  partial,
  failed,
  canceled,
}

class FileTransferTask {
  const FileTransferTask(this.id, this.done);

  final int id;
  final Future<FileTransferResult> done;
}

class FileTransferSnapshot {
  const FileTransferSnapshot({
    required this.id,
    required this.direction,
    required this.name,
    required this.status,
    required this.bytes,
    required this.totalBytes,
    required this.filesDone,
    required this.totalFiles,
    required this.bytesPerSecond,
  });

  final int id;
  final FileTransferDirection direction;
  final String name;
  final FileTransferStatus status;
  final int bytes;
  final int? totalBytes;
  final int filesDone;
  final int? totalFiles;
  final double bytesPerSecond;

  Duration? get remaining {
    final total = totalBytes;
    if (status != FileTransferStatus.running ||
        total == null ||
        bytesPerSecond <= 0) {
      return null;
    }
    if (bytes >= total) return Duration.zero;
    return Duration(seconds: ((total - bytes) / bytesPerSecond).ceil());
  }
}

class FileTransferResult {
  FileTransferResult({
    required this.id,
    required this.direction,
    required this.name,
    required this.status,
    required this.bytes,
    required this.files,
    required this.finishedAt,
    required this.elapsed,
    required Iterable<String> skipped,
    required Iterable<FileTransferFailure> failures,
    this.error,
  }) : skipped = List.unmodifiable(skipped),
       failures = List.unmodifiable(failures);

  final int id;
  final FileTransferDirection direction;
  final String name;
  final FileTransferStatus status;
  final int bytes;
  final int files;
  final DateTime finishedAt;
  final Duration elapsed;
  final List<String> skipped;
  final List<FileTransferFailure> failures;
  final FileTransferError? error;

  double get bytesPerSecond =>
      elapsed.inMicroseconds == 0 ? 0 : bytes * 1e6 / elapsed.inMicroseconds;
}
