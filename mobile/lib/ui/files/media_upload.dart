import 'dart:io';

import 'package:flutter/services.dart';

import '../../files/file_transfer_service.dart';

const _channel = MethodChannel('kkdesk/media_upload');
final _temporaryName = RegExp(
  r'^(?:image_picker(?:_.*)?|fullsizerender(?:-\d+)?|image|photo|video|file|\d{1,7}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:[-_].*)?)$',
  caseSensitive: false,
);
final _extension = RegExp(r'^[A-Za-z0-9]{1,12}$');

Future<List<UploadSource>> pickMediaForUpload() async {
  final picked = await _channel.invokeListMethod<Map<Object?, Object?>>('pick');
  final importedAt = DateTime.now();
  return [
    for (final item in picked ?? const <Map<Object?, Object?>>[])
      await _uploadSource(item, importedAt),
  ];
}

Future<UploadSource> _uploadSource(
  Map<Object?, Object?> item,
  DateTime importedAt,
) async {
  final file = File(item['path']! as String);
  final timestampMs = item['timestampMs'] as int?;
  final timestamp = timestampMs == null
      ? importedAt
      : DateTime.fromMillisecondsSinceEpoch(timestampMs);
  return UploadSource.file(
    name: mediaUploadName(
      item['name'] as String?,
      extension: item['extension'] as String? ?? '',
      video: item['video'] == true,
      timestamp: timestamp,
    ),
    size: await file.length(),
    modified: timestamp,
    openRead: file.openRead,
  );
}

String mediaUploadName(
  String? originalName, {
  required String extension,
  required bool video,
  required DateTime timestamp,
}) {
  final name = (originalName ?? '')
      .split(RegExp(r'[/\\]'))
      .last
      .replaceAll(RegExp(r'[<>:"|?*\x00-\x1f]'), '_')
      .trim();
  final dot = name.lastIndexOf('.');
  final candidate = dot > 0 ? name.substring(dot + 1) : '';
  final originalExtension = _extension.hasMatch(candidate) ? candidate : '';
  var stem = (originalExtension.isEmpty ? name : name.substring(0, dot))
      .replaceAll(RegExp(r'^\.+|[. ]+$'), '');
  final exportedExtension = _extension.hasMatch(extension) ? extension : '';
  final sameFormat =
      _canonicalExtension(originalExtension) ==
      _canonicalExtension(exportedExtension);

  if (stem.isEmpty || _temporaryName.hasMatch(stem)) {
    final local = timestamp.toLocal();
    String pad(int part) => part.toString().padLeft(2, '0');
    stem =
        '${video ? '视频' : '照片'}_${local.year}${pad(local.month)}'
        '${pad(local.day)}_${pad(local.hour)}${pad(local.minute)}${pad(local.second)}';
  }
  final suffix = exportedExtension.isEmpty || sameFormat
      ? originalExtension
      : exportedExtension.toLowerCase();
  return suffix.isEmpty ? stem : '$stem.$suffix';
}

String _canonicalExtension(String value) => switch (value.toLowerCase()) {
  'jpeg' => 'jpg',
  final lower => lower,
};
