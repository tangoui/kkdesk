import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/files/file_protocol.dart';
import 'package:kkdesk_mobile/files/file_transfer_service.dart';
import 'package:kkdesk_mobile/files/files_client.dart';

void main() {
  late Directory target;
  late FakeFileChannel channel;
  late FilesClient client;

  setUp(() async {
    target = await Directory.systemTemp.createTemp('kkdesk-files');
    channel = FakeFileChannel();
    client = FilesClient(channel);
  });

  tearDown(() async {
    await client.close();
    await channel.close();
    await target.delete(recursive: true);
  });

  test('fragmented listing preserves truncation and Unicode', () async {
    // Flutter delivers text as Unicode. A valid terminal frame starts at a
    // code-point boundary, even when binary fragments end inside a rune.
    channel.request = (m) {
      final json = encodeFileMessage({
        'id': m['id'],
        'ok': true,
        'listing': {
          'path': '/tmp',
          'entries': [
            {'name': '照片', 'path': '/tmp/照片', 'mtime': '2026-11-25T08:00:00Z'},
          ],
          'truncated': true,
        },
      });
      final bytes = utf8.encode(json);
      final split = bytes.indexOf(0xe7);
      channel.binary(
        FileFrame(fileJsonKind, 0, 0, bytes.sublist(0, split + 1)).encode(),
      );
      channel.binary(
        FileFrame(
          fileJsonKind,
          0,
          0,
          bytes.sublist(split + 1, split + 3),
        ).encode(),
      );
      channel.text(utf8.decode(bytes.sublist(split + 3)));
    };
    final listing = await client.list('/tmp');
    expect(listing.truncated, isTrue);
    expect(listing.entries.single.name, '照片');
  });

  test(
    'denied browse and permission revocation of an active download',
    () async {
      channel.request = (m) => channel.reply(m, error: 'denied');
      await expectLater(
        client.home(),
        throwsA(
          isA<FileTransferError>().having((e) => e.code, 'code', 'denied'),
        ),
      );
      channel.request = (m) {
        channel.beginDownload(m);
        final id = m['transfer'] as int;
        channel.notice('file', id, {'rel': 'photo.jpg', 'size': 3});
        channel.data(id, 0, 'a');
        channel.notice('abort', id, {'error': 'denied'});
      };
      final task = client.download('/photo.jpg', target);
      final result = await task.done;
      expect(result.status, FileTransferStatus.failed);
      expect(result.error?.code, 'denied');
      expect(await target.list().isEmpty, isTrue);
    },
  );

  test('cancel during download removes the part and sends cancel', () async {
    final started = Completer<int>();
    final canceled = Completer<int>();
    channel.request = (m) {
      final id = m['transfer'] as int;
      if (m['op'] == 'cancel') {
        canceled.complete(id);
        channel.reply(m);
        return;
      }
      channel.beginDownload(m);
      channel.notice('file', id, {'rel': 'photo.jpg', 'size': 3});
      channel.data(id, 0, 'a');
      started.complete(id);
    };
    final task = client.download('/photo.jpg', target);
    await started.future;
    expect((await target.list().single).path, endsWith('.kkdesk-part'));
    client.cancel(task.id);
    expect((await task.done).status, FileTransferStatus.canceled);
    expect(await canceled.future, await started.future);
    expect(await target.list().isEmpty, isTrue);
  });

  test('checksum mismatch removes the part', () async {
    channel.request = (m) {
      channel.beginDownload(m);
      final id = m['transfer'] as int;
      channel.file(id, 'photo.jpg', 'abc', checksum: 'invalid');
      channel.notice('done', id);
    };
    final result = await client.download('/photo.jpg', target).done;
    expect(result.status, FileTransferStatus.failed);
    expect(result.failures.single.error.code, 'checksum');
    expect(await target.list().isEmpty, isTrue);
  });

  test(
    'existing names are preserved and the smallest free suffix is used',
    () async {
      await File('${target.path}/photo.jpg').writeAsString('original');
      await File('${target.path}/photo (1).jpg').writeAsString('other');
      channel.request = (m) {
        channel.beginDownload(m);
        final id = m['transfer'] as int;
        channel.file(id, 'photo.jpg', 'abc');
        channel.notice('done', id);
      };
      final result = await client.download('/photo.jpg', target).done;
      expect(result.status, FileTransferStatus.completed);
      expect(result.name, 'photo (2).jpg');
      expect(await File('${target.path}/${result.name}').readAsString(), 'abc');
      expect(await File('${target.path}/photo.jpg').readAsString(), 'original');
      expect(client.history.single.bytes, 3);
    },
  );

  test(
    'a failed child does not prevent other folder files from arriving',
    () async {
      channel.request = (m) {
        final id = m['transfer'] as int;
        channel.reply(m, fields: {'name': 'album', 'size': 6, 'files': 2});
        channel.notice('dir', id, {'rel': 'album'});
        channel.file(id, 'album/bad', 'abc', checksum: 'invalid');
        channel.file(id, 'album/good', 'xyz');
        channel.notice('done', id);
      };
      final result = await client.download('/album', target).done;
      expect(result.status, FileTransferStatus.partial);
      expect(result.failures.single.path, 'album/bad');
      expect(result.bytes, 3);
      expect(await File('${target.path}/album/good').readAsString(), 'xyz');
      expect(await Directory('${target.path}/album').list().length, 1);
    },
  );

  test(
    'stream upload is chunked, waits for backpressure, and sends SHA-256',
    () async {
      final bytes = Uint8List(fileChunkSize + 3);
      final chunks = <FileFrame>[];
      final began = Completer<void>();
      channel.amount = fileHighWater + 1;
      channel.sentBinary = (b) => chunks.add(FileFrame.decode(b));
      channel.request = (m) {
        if (m['op'] == 'upload_begin') {
          expect(m['size'], bytes.length);
          began.complete();
        }
        if (m['op'] == 'end') {
          expect(m['sha256'], sha256.convert(bytes).toString());
          channel.reply(m, fields: {'name': 'upload (1).bin'});
        } else {
          channel.reply(m);
        }
      };
      final task = client.upload(
        UploadSource.file(
          name: 'upload.bin',
          size: bytes.length,
          openRead: () => Stream.value(bytes),
        ),
        '/remote',
      );
      await began.future;
      await Future<void>.delayed(Duration.zero);
      expect(chunks, isEmpty);
      expect(channel.lowThreshold, fileLowWater);
      channel.amount = 0;
      channel.low.add(null);
      final result = await task.done;
      expect(result.status, FileTransferStatus.completed);
      expect(result.name, 'upload (1).bin');
      expect(chunks.map((c) => c.offset), [0, fileChunkSize]);
      expect(chunks.map((c) => c.payload.length), [fileChunkSize, 3]);
    },
  );

  test(
    'folder upload cleans up a failed source and sends the next file',
    () async {
      final ended = <FileMessage>[];
      channel.request = (message) {
        final op = message['op'];
        if (op == 'mkdir') {
          channel.reply(message, fields: {'name': 'album (1)'});
        } else if (op == 'end') {
          ended.add(message);
          channel.reply(
            message,
            error: message['error'] as String?,
            fields: {'name': 'good'},
          );
        } else {
          channel.reply(message);
        }
      };
      final result = await client
          .upload(
            UploadSource.directory(
              name: 'album',
              listChildren: () async => [
                UploadSource.file(
                  name: 'bad',
                  size: 3,
                  openRead: () => Stream.value([1]),
                ),
                UploadSource.file(
                  name: 'good',
                  size: 3,
                  openRead: () => Stream.value([1, 2, 3]),
                ),
              ],
            ),
            '/remote',
          )
          .done;
      expect(result.status, FileTransferStatus.partial);
      expect(result.name, 'album (1)');
      expect(result.failures.single.path, 'album/bad');
      expect(result.bytes, 3);
      expect(ended.map((m) => m['rel']), ['album (1)/bad', 'album (1)/good']);
      expect(ended.first['error'], 'checksum');
      expect(ended.last['sha256'], sha256.convert([1, 2, 3]).toString());
    },
  );
}

class FakeFileChannel implements FileChannel {
  final incoming = StreamController<FileChannelMessage>(sync: true);
  final low = StreamController<void>.broadcast(sync: true);
  void Function(FileMessage) request = (_) {};
  void Function(Uint8List) sentBinary = (_) {};
  int amount = 0;
  int lowThreshold = 0;

  @override
  Stream<FileChannelMessage> get messages => incoming.stream;
  @override
  Stream<void> get bufferedAmountLow => low.stream;
  @override
  set bufferedAmountLowThreshold(int amount) => lowThreshold = amount;
  @override
  Future<int> bufferedAmount() async => amount;
  @override
  Future<void> sendBinary(Uint8List bytes) async => sentBinary(bytes);
  @override
  Future<void> sendText(String text) async {
    final message = decodeFileMessage(utf8.encode(text));
    if (message['op'] == 'hello') {
      reply(message, fields: {'version': 1});
    } else {
      request(message);
    }
  }

  Future<void> close() async {
    await incoming.close();
    await low.close();
  }

  void text(String text) => incoming.add(FileChannelMessage.text(text));
  void binary(Uint8List bytes) =>
      incoming.add(FileChannelMessage.binary(bytes));
  void json(FileMessage message) => text(encodeFileMessage(message));
  void reply(FileMessage m, {String? error, FileMessage fields = const {}}) =>
      json({'id': m['id'], 'ok': error == null, 'error': ?error, ...fields});
  void notice(String op, int id, [FileMessage fields = const {}]) =>
      json({'op': op, 'transfer': id, ...fields});
  void beginDownload(FileMessage m) =>
      reply(m, fields: {'name': 'photo.jpg', 'size': 3, 'files': 1});
  void data(int id, int offset, String text) =>
      binary(FileFrame(fileDataKind, id, offset, utf8.encode(text)).encode());
  void file(int id, String rel, String text, {String? checksum}) {
    final bytes = utf8.encode(text);
    notice('file', id, {'rel': rel, 'size': bytes.length});
    data(id, 0, text);
    notice('end', id, {
      'rel': rel,
      'sha256': checksum ?? sha256.convert(bytes).toString(),
    });
  }
}
