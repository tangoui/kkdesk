import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/lan_protocol.dart';

void main() {
  test('answers the Go HMAC-SHA256 challenge', () {
    final nonce = base64Encode(List<int>.generate(32, (index) => index));
    expect(
      challengeMac('密码', nonce),
      'cEeVmODIs9N6TF5di1y2JoPpRCqfSJn+XoWSkZruKfw=',
    );
  });

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
