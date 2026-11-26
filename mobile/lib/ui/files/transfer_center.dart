import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../files/file_transfer_service.dart';
import '../../lan_auth.dart';
import '../../lan_session.dart';

String transferError(Object? error) {
  if (error is LanAuthFailure) return error.message;
  if (error is SocketException) return '无法连接电脑，请检查地址和网络';
  if (error is FormatException) return '电脑地址或连接响应无效';
  if (error is StateError) return '连接被拒绝，请检查密码和电脑端确认';
  return switch (error is FileTransferError ? error.code : '') {
    'denied' => '电脑关闭了文件传输权限',
    'permission' => '没有权限访问此位置',
    'not_found' => '找不到文件或文件夹',
    'closed' || 'disconnected' => '连接已断开',
    'timeout' => '操作超时，请重试',
    'checksum' => '校验失败，请重试',
    'canceled' => '已取消',
    'exists' => '该位置已有同名项目',
    'invalid' => '文件名或路径无效',
    'version' => '电脑端版本不支持文件传输',
    _ => '无法读取或写入文件，请检查可用空间和文件权限',
  };
}

String byteCount(num count) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var value = count.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return '${value.toStringAsFixed(unit == 0 || value >= 10 ? 0 : 1)} ${units[unit]}';
}

class TransferConnection {
  TransferConnection({
    required this.address,
    required this.service,
    this.firstHostCode,
    this.onClose,
    this.reopen,
  });

  final String address;
  final FileTransferService service;
  final String? firstHostCode;
  final Future<void> Function()? onClose;
  final Future<TransferConnection> Function()? reopen;
  bool closed = false;
  int browsers = 0;
}

class TransferRecord {
  TransferRecord({
    required this.address,
    required this.name,
    required this.direction,
    required this.status,
    required this.time,
    required this.bytes,
    this.detail,
    this.localFolder,
    this.retry,
  });

  final String address;
  final String name;
  final FileTransferDirection direction;
  final FileTransferStatus status;
  final DateTime time;
  final int bytes;
  final String? detail;
  final String? localFolder;
  final Future<void> Function()? retry;

  Map<String, Object?> toJson() => {
    'address': address,
    'name': name,
    'direction': direction.name,
    'status': status.name,
    'time': time.toIso8601String(),
    'bytes': bytes,
    'detail': detail,
    'localFolder': localFolder,
  };

  factory TransferRecord.fromJson(Map<String, dynamic> json) => TransferRecord(
    address: json['address'] as String,
    name: json['name'] as String,
    direction: FileTransferDirection.values.byName(json['direction'] as String),
    status: FileTransferStatus.values.byName(json['status'] as String),
    time: DateTime.parse(json['time'] as String),
    bytes: json['bytes'] as int,
    detail: json['detail'] as String?,
    localFolder: json['localFolder'] as String?,
  );
}

class ActiveTransfer {
  const ActiveTransfer(this.address, this.name, this.snapshot, this.cancel);
  final String address;
  final String name;
  final FileTransferSnapshot snapshot;
  final VoidCallback cancel;
}

class TransferCenter extends ChangeNotifier {
  TransferCenter({Future<Directory> Function()? documents, this._preferences})
    : _documents = documents ?? getApplicationDocumentsDirectory {
    _loading = _load();
  }

  final Future<Directory> Function() _documents;
  final SharedPreferencesAsync? _preferences;
  final _connections =
      <TransferConnection, StreamSubscription<List<FileTransferSnapshot>>>{};
  final _standalone = <String, Future<TransferConnection>>{};
  final _jobs = <(TransferConnection, int), String>{};
  final _records = <TransferRecord>[];
  late final Future<void> _loading;
  Future<void> _saving = Future.value();
  bool _disposed = false;
  static const _historyKey = 'files.ui.history.v1';

  List<ActiveTransfer> get active => [
    for (final connection in _connections.keys)
      for (final snapshot in connection.service.queue)
        ActiveTransfer(
          connection.address,
          _jobs[(connection, snapshot.id)] ?? snapshot.name,
          snapshot,
          () => connection.service.cancel(snapshot.id),
        ),
  ];

  List<TransferRecord> get records => List.unmodifiable(_records);

  Future<void> _load() async {
    try {
      final saved =
          await _preferences?.getStringList(_historyKey) ?? const <String>[];
      for (final item in saved) {
        try {
          _records.add(
            TransferRecord.fromJson(jsonDecode(item) as Map<String, dynamic>),
          );
        } catch (_) {
          // A damaged row should not hide the remaining history.
        }
      }
      _changed();
    } catch (_) {
      // File operations can still proceed with an in-memory history.
    }
  }

  Future<Directory> downloadRoot() async {
    final documents = await _documents();
    return Directory('${documents.path}/Downloads')
      ..createSync(recursive: true);
  }

  Future<TransferConnection> connect(String address, String password) =>
      _connect(
        address,
        (session) =>
            session.connect(address, password, mode: LanSessionMode.files),
      );

  Future<TransferConnection> connectPaired(
    String id,
    String name,
    PairedConnection connect,
  ) => _connect(
    id,
    (session) =>
        session.connectPaired(name, connect, mode: LanSessionMode.files),
  );

  Future<TransferConnection> _connect(
    String address,
    Future<void> Function(LanSession) dial,
  ) {
    return _standalone.putIfAbsent(address, () async {
      final session = LanSession(onStream: (_) {}, onEnded: (_) {});
      try {
        await dial(session);
        final connection = TransferConnection(
          address: address,
          service: await session.files,
          firstHostCode: session.firstHostCode,
          onClose: session.close,
          reopen: () => _connect(address, dial),
        );
        if (_disposed) throw const FileTransferError('closed');
        attach(connection);
        return connection;
      } catch (_) {
        _standalone.remove(address);
        await session.close();
        rethrow;
      }
    });
  }

  void openBrowser(TransferConnection connection) {
    connection.browsers++;
    attach(connection);
  }

  void closeBrowser(TransferConnection connection) {
    connection.browsers--;
    _closeIfIdle(connection);
  }

  void _closeIfIdle(TransferConnection connection) {
    if (connection.closed ||
        connection.onClose == null ||
        connection.browsers != 0 ||
        connection.service.queue.isNotEmpty ||
        _jobs.keys.any((job) => identical(job.$1, connection))) {
      return;
    }
    connection.closed = true;
    _standalone.remove(connection.address);
    unawaited(connection.onClose!());
  }

  void attach(TransferConnection connection) {
    if (_disposed || _connections.containsKey(connection)) return;
    _connections[connection] = connection.service.queueChanges.listen(
      (_) => _changed(),
      onDone: () {
        if (connection.onClose != null && !connection.closed) {
          _standalone.remove(connection.address);
          unawaited(connection.onClose!());
        }
        connection.closed = true;
        final subscription = _connections.remove(connection);
        unawaited(subscription?.cancel());
        _changed();
      },
    );
    unawaited(_importHistory(connection));
    _changed();
  }

  Future<void> _importHistory(TransferConnection connection) async {
    await _loading;
    if (_disposed) return;
    var imported = false;
    for (final item in connection.service.history) {
      final duplicate = _records.any(
        (row) =>
            row.address == connection.address &&
            row.time == item.finishedAt &&
            row.direction == item.direction &&
            row.name == item.name,
      );
      if (duplicate) continue;
      imported = true;
      _records.add(_record(connection, item, item.name));
    }
    if (imported) _save();
  }

  Future<void> download(
    TransferConnection connection,
    RemoteEntry entry,
  ) async {
    final root = await downloadRoot();
    final folder = await root.createTemp('传输-');
    final task = connection.service.download(entry.path, folder);
    _watch(
      connection,
      task,
      entry.name,
      folder.path.split(Platform.pathSeparator).last,
      (next) => download(next, entry),
    );
  }

  void upload(
    TransferConnection connection,
    UploadSource source,
    String remoteDirectory,
  ) {
    final task = connection.service.upload(source, remoteDirectory);
    _watch(
      connection,
      task,
      source.name,
      null,
      (next) async => upload(next, source, remoteDirectory),
    );
  }

  void _watch(
    TransferConnection connection,
    FileTransferTask task,
    String requestedName,
    String? folder,
    Future<void> Function(TransferConnection) repeat,
  ) {
    final key = (connection, task.id);
    _jobs[key] = requestedName;
    _changed();
    unawaited(() async {
      final result = await task.done;
      _jobs.remove(key);
      _closeIfIdle(connection);
      final localFolder =
          result.status == FileTransferStatus.completed ||
              result.status == FileTransferStatus.partial ||
              result.files > 0
          ? folder
          : null;
      if (folder != null && localFolder == null) {
        try {
          final root = await downloadRoot();
          final temporary = Directory('${root.path}/$folder');
          if (await temporary.exists()) await temporary.delete(recursive: true);
        } on FileSystemException {
          // Cleanup cannot change the transfer outcome.
        }
      }
      await _loading;
      if (_disposed) return;
      _records.insert(
        0,
        _record(
          connection,
          result,
          requestedName,
          folder: localFolder,
          retry: () async {
            final current = connection.closed
                ? await (connection.reopen?.call() ??
                      Future.error(const FileTransferError('closed')))
                : connection;
            await repeat(current);
          },
        ),
      );
      _save();
    }());
  }

  TransferRecord _record(
    TransferConnection connection,
    FileTransferResult result,
    String requestedName, {
    String? folder,
    Future<void> Function()? retry,
  }) {
    final message = [
      if ((result.status == FileTransferStatus.completed ||
              result.status == FileTransferStatus.partial) &&
          requestedName != result.name)
        '重名已自动改名为「${result.name}」',
      if (result.error case final error?) transferError(error),
      if (result.error == null && result.failures.isNotEmpty)
        transferError(result.failures.first.error),
      if (result.failures.length > 1) '${result.failures.length} 个项目未传输',
      if (result.skipped.isNotEmpty) '已跳过 ${result.skipped.length} 个项目',
    ];
    return TransferRecord(
      address: connection.address,
      name: result.name,
      direction: result.direction,
      status: result.status,
      time: result.finishedAt,
      bytes: result.bytes,
      detail: message.isEmpty ? null : message.join(' · '),
      localFolder: folder,
      retry:
          result.status == FileTransferStatus.failed ||
              result.status == FileTransferStatus.partial
          ? retry
          : null,
    );
  }

  void _save() {
    _records.sort((a, b) => b.time.compareTo(a.time));
    if (_records.length > 100) _records.removeRange(100, _records.length);
    final saved = [for (final record in _records) jsonEncode(record.toJson())];
    _saving = _saving
        .then((_) async {
          await _preferences?.setStringList(_historyKey, saved);
        })
        .catchError((Object _) {});
    _changed();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    for (final entry in _connections.entries) {
      unawaited(entry.value.cancel());
      unawaited(entry.key.onClose?.call());
    }
    _connections.clear();
    super.dispose();
  }
}
