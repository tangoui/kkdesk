import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter_driver/driver_extension.dart';
import 'package:image_picker/image_picker.dart';
import 'package:kkdesk_mobile/files/file_transfer_service.dart';
import 'package:kkdesk_mobile/lan_session.dart';

const _address = String.fromEnvironment('KKDESK_FILE_TEST_ADDRESS');
const _password = String.fromEnvironment('KKDESK_FILE_TEST_PASSWORD');
const _root = String.fromEnvironment('KKDESK_FILE_TEST_ROOT');
const _pickPhoto = bool.fromEnvironment('KKDESK_FILE_TEST_PICK_PHOTO');

void main() {
  final status = ValueNotifier('正在连接电脑…');
  final result = Completer<String>();
  enableFlutterDriverExtension(handler: (_) => result.future);
  runApp(
    MaterialApp(
      home: Scaffold(
        body: Center(
          child: ValueListenableBuilder(
            valueListenable: status,
            builder: (_, message, _) => Text(message),
          ),
        ),
      ),
    ),
  );
  WidgetsBinding.instance.addPostFrameCallback((_) async {
    try {
      result.complete(jsonEncode(await _verify(status)));
      status.value = '文件传输验证通过';
    } catch (error, stack) {
      status.value = '文件传输验证失败\n$error';
      result.complete(jsonEncode({'error': '$error\n$stack'}));
    }
  });
}

Future<Map<String, Object>> _verify(ValueNotifier<String> status) async {
  final local = await Directory.systemTemp.createTemp('kkdesk-files-smoke');
  final session = LanSession(
    onStream: (_) => throw StateError('Files-only mode received a video track'),
    onEnded: (reason) => debugPrint(reason),
  );
  final results = <Map<String, Object>>[];
  final outputs = <String, Object>{};

  Future<void> complete(FileTransferTask task, String label) async {
    final result = await task.done;
    if (result.status != FileTransferStatus.completed) {
      throw StateError('$label: ${result.error}, ${result.failures}');
    }
    results.add({
      'operation': label,
      'bytes': result.bytes,
      'files': result.files,
      'seconds': result.elapsed.inMicroseconds / 1e6,
      'MBps': result.bytesPerSecond / 1e6,
    });
  }

  Future<Map<String, Object>> digest(File file) async => {
    'bytes': await file.length(),
    'sha256': (await sha256.bind(file.openRead()).first).toString(),
  };

  try {
    await session.connect(_address, _password, mode: LanSessionMode.files);
    final service = await session.files;
    await service.home();
    final source = '$_root/source';
    final target = '$_root/received';
    final listing = await service.list(source);
    for (final entry in listing.entries) {
      status.value = '正在下载 ${entry.name}…';
      final remote = await service.stat(entry.path);
      await complete(
        service.download(remote.path, local),
        'download ${entry.name}',
      );
    }
    for (final relative in [
      'big.bin',
      'folder/你好.txt',
      'folder/nested/empty',
    ]) {
      outputs['download $relative'] = await digest(
        File('${local.path}/$relative'),
      );
    }
    if (_pickPhoto) {
      status.value = '请选择一张照片用于上传验证';
      final photo = await ImagePicker().pickImage(source: ImageSource.gallery);
      if (photo == null) throw StateError('No photo selected');
      final upload = UploadSource.file(
        name: 'photo.jpg',
        size: await photo.length(),
        openRead: photo.openRead,
      );
      status.value = '正在上传照片…';
      await complete(service.upload(upload, target), 'upload photo.jpg');
      outputs['upload photo.jpg'] = await digest(File(photo.path));
    } else {
      status.value = '正在上传文件…';
      final file = File('${local.path}/folder/你好.txt');
      await complete(
        service.upload(await UploadSource.fromPath(file.path), target),
        'upload 你好.txt',
      );
      outputs['upload 你好.txt'] = await digest(file);
    }
    status.value = '正在上传文件夹…';
    await complete(
      service.upload(
        await UploadSource.fromPath('${local.path}/folder'),
        target,
      ),
      'upload folder',
    );
    final uploaded = await service.list('$target/folder');
    if (uploaded.entries.length != 2) {
      throw StateError('Unexpected upload listing');
    }
    return {'transfers': results, 'digests': outputs};
  } finally {
    await session.close();
    await local.delete(recursive: true);
  }
}
