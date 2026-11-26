import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/lan_protocol.dart';

void main() {
  test(
    'decodes Go accessibility status fixtures and ignores other metadata',
    () {
      for (final (name, expected) in [('denied', false), ('granted', true)]) {
        final bytes = File('test/fixtures/meta/accessibility-$name.bin')
            .readAsBytesSync();
        expect(decodeAccessibility(bytes), expected);
      }
      for (final bytes in <List<int>>[
        [],
        [0x61],
        [0x61, 2],
        [0x70, 0],
      ]) {
        expect(decodeAccessibility(bytes), isNull);
      }
    },
  );

  test('encodes normalized mouse events as Go float32 little endian', () {
    expect(mouseMove(0.25, 0.75), [1, 0, 0, 128, 62, 0, 0, 64, 63]);
    expect(mouseButton(0.25, 0.75, true), [
      2,
      0,
      0,
      128,
      62,
      0,
      0,
      64,
      63,
      0,
      1,
      0,
    ]);
  });
}
