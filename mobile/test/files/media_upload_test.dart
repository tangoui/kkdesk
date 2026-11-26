import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/ui/files/media_upload.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final imported = DateTime(2026, 11, 25, 19, 31, 1);

  test('preserves original names and matches the exported format', () {
    expect(
      mediaUploadName(
        'IMG_1234.HEIC',
        extension: 'heic',
        video: false,
        timestamp: imported,
      ),
      'IMG_1234.HEIC',
    );
    expect(
      mediaUploadName(
        'IMG_1234.HEIC',
        extension: 'jpg',
        video: false,
        timestamp: imported,
      ),
      'IMG_1234.jpg',
    );
    expect(
      mediaUploadName(
        '假期短片.MOV',
        extension: 'mov',
        video: true,
        timestamp: imported,
      ),
      '假期短片.MOV',
    );
  });

  test('uses localized import-date names for anonymous photos and videos', () {
    expect(
      mediaUploadName(
        'image_picker_F9EB4DA5-1234.jpg',
        extension: 'jpg',
        video: false,
        timestamp: imported,
      ),
      '照片_20261125_193101.jpg',
    );
    expect(
      mediaUploadName(
        '24.mp4',
        extension: 'mp4',
        video: true,
        timestamp: imported,
      ),
      '视频_20261125_193101.mp4',
    );
    expect(
      mediaUploadName(
        null,
        extension: 'heic',
        video: false,
        timestamp: imported,
      ),
      '照片_20261125_193101.heic',
    );
  });

  test('uses supplied original names even if they look generic', () {
    for (final name in ['photo.JPG', '24.JPG', 'file.JPG']) {
      expect(
        mediaUploadName(
          name,
          extension: 'jpg',
          video: false,
          timestamp: imported,
          isOriginalName: true,
        ),
        name,
      );
    }
  });

  test(
    'uses a useful exported filename when the suggested name is generic',
    () {
      expect(
        mediaUploadName(
          'image.jpg',
          exportedName: 'IMG_4321.JPG',
          extension: 'jpg',
          video: false,
          timestamp: imported,
        ),
        'IMG_4321.JPG',
      );
    },
  );

  test('upload uses selected metadata and reads the exported copy', () async {
    final directory = Directory.systemTemp.createTempSync('kkdesk-media-test');
    addTearDown(() => directory.deleteSync(recursive: true));
    final selected = File('${directory.path}/image_picker_cache.jpeg')
      ..writeAsBytesSync([1, 2, 3]);
    const channel = MethodChannel('kkdesk/media_upload');
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'pick');
      return [
        {
          'path': selected.path,
          'name': 'photo.JPG',
          'isOriginalName': true,
          'extension': 'jpeg',
          'video': false,
          'timestampMs': imported.millisecondsSinceEpoch,
        },
        {
          'path': selected.path,
          'extension': 'jpeg',
          'video': false,
          'timestampMs': imported.millisecondsSinceEpoch,
        },
      ];
    });
    final sources = await pickMediaForUpload();
    expect(sources.map((source) => source.name), [
      'photo.JPG',
      '照片_20261125_193101.jpeg',
    ]);
    expect(sources.first.modified, imported);
    expect(sources.first.size, 3);
    expect(await sources.first.openRead!().expand((chunk) => chunk).toList(), [
      1,
      2,
      3,
    ]);
  });
}
