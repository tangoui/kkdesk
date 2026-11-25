import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/files/file_protocol.dart';

void main() {
  final fixtures = Directory('test/fixtures/files');

  test('Go JSON messages round-trip byte for byte', () {
    for (final file in fixtures.listSync().whereType<File>()) {
      if (!file.path.endsWith('.json')) continue;
      final bytes = file.readAsBytesSync();
      expect(
        utf8.encode(encodeFileMessage(decodeFileMessage(bytes))),
        bytes,
        reason: file.path,
      );
    }
    expect(
      encodeFileMessage({'id': 1, 'op': 'hello', 'version': 1}),
      File('${fixtures.path}/hello.json').readAsStringSync(),
    );
  });

  test('Go binary header and payload match in both directions', () {
    final bytes = File('${fixtures.path}/chunk.bin').readAsBytesSync();
    final frame = FileFrame.decode(bytes);
    expect(frame.kind, fileDataKind);
    expect(frame.transfer, 0x0102030405060708);
    expect(frame.offset, 0x1112131415161718);
    expect(frame.payload, utf8.encode('abc'));
    expect(frame.encode(), bytes);
    final fragment = File('${fixtures.path}/json_fragment.bin')
        .readAsBytesSync();
    final decoded = FileFrame.decode(fragment);
    expect(decoded.kind, fileJsonKind);
    expect(decoded.transfer, 0);
    expect(decoded.offset, 0);
    expect(decoded.encode(), fragment);
  });
}
