import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';

void main() {
  test('rejects an invitation with ambiguous or unsafe server settings', () {
    const base =
        'kkdesk://pair?v=1&id=AAAAAAAAAAAAAAAAAAAAAA'
        '&c=one_time_code_abcdefghijklmnopqrstuvwxyz'
        '&fp=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
    expect(PairTarget.parse('$base&lan=192.168.1.2%3A21118').lan, [
      '192.168.1.2:21118',
    ]);
    expect(
      () => PairTarget.parse('$base&v=1&lan=192.168.1.2%3A21118'),
      throwsFormatException,
    );
    expect(
      () => PairTarget.parse(
        '$base&s=http%3A%2F%2F192.168.1.2'
        '&t=12345678901234567890123456789012',
      ),
      throwsFormatException,
    );
  });
}
