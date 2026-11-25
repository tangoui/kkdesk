import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/files/file_transfer_service.dart';
import 'package:kkdesk_mobile/ui/files/file_browser.dart';
import 'package:kkdesk_mobile/ui/files/transfer_center.dart';
import 'package:kkdesk_mobile/ui/files/transfer_rows.dart';

void main() {
  test('file transfer errors are translated without wire details', () {
    expect(
      transferError(const FileTransferError('denied', 'private server detail')),
      '电脑关闭了文件传输权限',
    );
    expect(transferError(const FileTransferError('not_found')), '找不到文件或文件夹');
    expect(transferError(const FileTransferError('checksum')), '校验失败，请重试');
    expect(transferError(const FileTransferError('closed')), '连接已断开');
  });

  testWidgets(
    'browser navigates and starts download for multiple selected entries',
    (tester) async {
      final directory = Directory.systemTemp.createTempSync(
        'kkdesk-test-download',
      );
      addTearDown(() => directory.deleteSync(recursive: true));
      final service = _Files();
      final center = TransferCenter(documents: () async => directory);
      addTearDown(center.dispose);
      final connection = TransferConnection(
        address: 'fixture',
        service: service,
      );
      await tester.pumpWidget(
        MaterialApp(
          home: FileBrowser(connect: () async => connection, center: center),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('照片'), findsOneWidget);
      expect(find.textContaining('只返回了部分'), findsOneWidget);

      await tester.tap(find.text('照片'));
      await tester.pumpAndSettle();
      expect(find.text('相片.jpg'), findsOneWidget);
      await tester.tap(find.byTooltip('上一级'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('多选'));
      await tester.tap(find.text('照片'));
      await tester.tap(find.text('说明.txt'));
      await tester.pumpAndSettle();
      expect(find.text('下载到手机（2）'), findsOneWidget);
      await tester.runAsync(() async {
        await tester.tap(find.text('下载到手机（2）'));
        await Future<void>.delayed(const Duration(milliseconds: 80));
      });
      await tester.pumpAndSettle();
      expect(service.downloads, unorderedEquals(['/home/照片', '/home/说明.txt']));
      expect(find.text('传输队列'), findsOneWidget);
    },
  );

  testWidgets('browser shows a friendly permission error', (tester) async {
    final service = _Files()..denied = true;
    final center = TransferCenter();
    addTearDown(center.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: FileBrowser(
          connect: () async =>
              TransferConnection(address: 'fixture', service: service),
          center: center,
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('电脑关闭了文件传输权限'), findsOneWidget);
    expect(find.text('denied'), findsNothing);
  });

  testWidgets('queue shows speed, cancel, completed, failed, and retry', (
    tester,
  ) async {
    final service = _Files();
    final center = TransferCenter();
    addTearDown(center.dispose);
    final connection = TransferConnection(address: 'fixture', service: service);
    await tester.pumpWidget(MaterialApp(home: FileQueuePage(center: center)));
    center.attach(connection);
    for (final name in ['已传.jpg', '校验.jpg', '取消.jpg']) {
      center.upload(
        connection,
        UploadSource.file(
          name: name,
          size: 4096,
          openRead: () => const Stream.empty(),
        ),
        '/home',
      );
    }
    await tester.pump();
    expect(find.textContaining('1.0 KB/秒'), findsWidgets);
    expect(find.textContaining('剩余 3 秒'), findsWidgets);
    expect(find.text('取消'), findsNWidgets(3));

    service.finish(1, FileTransferStatus.completed, actual: '已传 (1).jpg');
    service.finish(
      2,
      FileTransferStatus.failed,
      error: const FileTransferError('checksum'),
    );
    await tester.tap(find.text('取消').last);
    await tester.pumpAndSettle();
    expect(find.textContaining('已完成'), findsOneWidget);
    expect(find.textContaining('重名已自动改名'), findsOneWidget);
    expect(find.textContaining('已取消'), findsOneWidget);
    expect(find.text('校验失败，请重试'), findsOneWidget);
    await tester.tap(find.text('重试'));
    await tester.pumpAndSettle();
    expect(service.uploads.where((name) => name == '校验.jpg'), hasLength(2));
  });
}

class _Files implements FileTransferService {
  bool denied = false;
  final downloads = <String>[];
  final uploads = <String>[];
  final _queue = <FileTransferSnapshot>[];
  final _updates = StreamController<List<FileTransferSnapshot>>.broadcast();
  final _tasks = <int, Completer<FileTransferResult>>{};
  int _next = 0;

  RemoteEntry _entry(String parent, String name, {bool dir = false}) =>
      RemoteEntry.fromMessage({
        'name': name,
        'path': '$parent/$name',
        'is_dir': dir,
        'size': dir ? 0 : 32,
        'mtime': '2026-11-25T10:00:00Z',
      });

  @override
  Future<RemoteHome> home() async {
    if (denied) throw const FileTransferError('denied');
    return RemoteHome.fromMessage({
      'dir': '/home',
      'volumes': ['/'],
      'sep': '/',
      'os': 'darwin',
    });
  }

  @override
  Future<RemoteListing> list(String path) async {
    final entries = path == '/home'
        ? [_entry(path, '说明.txt'), _entry(path, '照片', dir: true)]
        : [_entry(path, '相片.jpg')];
    return RemoteListing.fromMessage({
      'path': path,
      'parent': path == '/home' ? '/' : '/home',
      'truncated': path == '/home',
      'entries': [
        for (final entry in entries)
          {
            'name': entry.name,
            'path': entry.path,
            'is_dir': entry.isDirectory,
            'size': entry.size,
            'mtime': entry.modified.toIso8601String(),
          },
      ],
    });
  }

  @override
  Future<RemoteEntry> stat(String path) async =>
      _entry('/home', path.split('/').last);
  @override
  Future<void> mkdir(String path) async {}
  @override
  List<FileTransferSnapshot> get queue => List.unmodifiable(_queue);
  @override
  Stream<List<FileTransferSnapshot>> get queueChanges => _updates.stream;
  @override
  List<FileTransferResult> get history => const [];

  FileTransferTask _add(String name, FileTransferDirection direction) {
    final id = ++_next;
    final result = _tasks[id] = Completer<FileTransferResult>();
    _queue.add(
      FileTransferSnapshot(
        id: id,
        direction: direction,
        name: name,
        status: FileTransferStatus.running,
        bytes: 1024,
        totalBytes: 4096,
        filesDone: 0,
        totalFiles: 1,
        bytesPerSecond: 1024,
      ),
    );
    _updates.add(queue);
    return FileTransferTask(id, result.future);
  }

  @override
  FileTransferTask download(String path, Directory localDirectory) {
    downloads.add(path);
    return _add(path.split('/').last, FileTransferDirection.download);
  }

  @override
  FileTransferTask upload(UploadSource source, String remoteDirectory) {
    uploads.add(source.name);
    return _add(source.name, FileTransferDirection.upload);
  }

  @override
  void cancel(int id) => finish(id, FileTransferStatus.canceled);

  void finish(
    int id,
    FileTransferStatus status, {
    FileTransferError? error,
    String? actual,
  }) {
    final entry = _queue.firstWhere((entry) => entry.id == id);
    _queue.remove(entry);
    _updates.add(queue);
    _tasks
        .remove(id)!
        .complete(
          FileTransferResult(
            id: id,
            direction: entry.direction,
            name: actual ?? entry.name,
            status: status,
            bytes: 1024,
            files: 1,
            finishedAt: DateTime.now(),
            elapsed: const Duration(seconds: 1),
            skipped: [],
            failures: [],
            error: error,
          ),
        );
  }
}
