import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/desktop_meta.dart';

void main() {
  test('requests use the Go meta bytes', () {
    expect(encodePrivacyRequest(true), [0x90, 1]);
    expect(encodePrivacyRequest(false), [0x90, 0]);
    expect(encodeLockAfterDisconnectRequest(true), [0x92, 1]);
    expect(encodeLockAfterDisconnectRequest(false), [0x92, 0]);
    expect(encodeLockNowRequest(), [0x94]);
    expect(encodeUnlockRequest(Uint8List.fromList([0xe5, 0xaf, 0x86, 0x31])), [
      0x99,
      0xe5,
      0xaf,
      0x86,
      0x31,
    ]);
  });

  test('malformed unlock bytes cannot be encoded', () {
    for (final bytes in <List<int>>[
      [],
      [0x31, 0],
      [0xff],
      List.filled(1025, 0x31),
    ]) {
      expect(encodeUnlockRequest(Uint8List.fromList(bytes)), isNull);
    }
    expect(
      encodeUnlockRequest(Uint8List(1024)..fillRange(0, 1024, 0x31)),
      hasLength(1025),
    );
  });

  test('accepts the Go privacy reply statuses and UTF-8 detail', () {
    final waiting = DesktopMeta.decode([0x91, 2]) as PrivacyReply;
    expect(waiting.kind, PrivacyReplyKind.screen);
    expect(waiting.status, PrivacyStatus.waiting);
    final off = DesktopMeta.decode([0x93, 0]) as PrivacyReply;
    expect(off.kind, PrivacyReplyKind.lockAfterDisconnect);
    expect(off.status, PrivacyStatus.off);
    final failure = DesktopMeta.decode([
      0x95,
      7,
      0xe9,
      0x94,
      0x81,
      0xe5,
      0xb1,
      0x8f,
      0xe5,
      0xa4,
      0xb1,
      0xe8,
      0xb4,
      0xa5,
    ]) as PrivacyReply;
    expect(failure.kind, PrivacyReplyKind.lockNow);
    expect(failure.status, PrivacyStatus.failed);
    expect(failure.detail, '锁屏失败');
    expect(
      (DesktopMeta.decode([0x95, 1]) as PrivacyReply).status,
      PrivacyStatus.on,
    );
  });

  test('rejects wrong direction, state, length and detail', () {
    for (final bytes in <List<int>>[
      [],
      [0x90, 1],
      [0x92, 0],
      [0x94],
      [0x99, 0x31],
      [0x91],
      [0x91, 7],
      [0x91, 1, 0x31],
      [0x91, 5, 0xff],
      [0x91, 5, ...List.filled(2049, 0x31)],
      [0x93, 2],
      [0x95, 0],
      [0x98, 0],
      [0x98, 11],
      [0x98, 2, 0],
      [0x9a, 0],
      [0x9a, 5],
      [0x9a, 1, 0],
      [0x9b],
      [0x9b, 0, 0],
    ]) {
      expect(
        DesktopMeta.decode(bytes),
        isNull,
        reason: 'kind ${bytes.firstOrNull}',
      );
    }
  });

  test('decodes all lock states and unlock results in wire order', () {
    const states = [
      LockScreenState.unlocked,
      LockScreenState.ready,
      LockScreenState.componentUnavailable,
      LockScreenState.platformUnsupported,
      LockScreenState.signatureRequired,
      LockScreenState.wrongInstallLocation,
      LockScreenState.serviceUnavailable,
      LockScreenState.componentNeedsUpdate,
      LockScreenState.componentError,
      LockScreenState.approvalRequired,
    ];
    for (final (index, state) in states.indexed) {
      expect(
        (DesktopMeta.decode([0x98, index + 1]) as LockScreenUpdate).state,
        state,
      );
    }
    const results = [
      UnlockResult.succeeded,
      UnlockResult.stillLocked,
      UnlockResult.unsupported,
      UnlockResult.tooMany,
    ];
    for (final (index, result) in results.indexed) {
      expect(
        (DesktopMeta.decode([0x9a, index + 1]) as UnlockReply).result,
        result,
      );
    }
  });

  test('decodes all Go cursors and falls back to the arrow', () {
    const shapes = [
      RemoteCursor.arrow,
      RemoteCursor.pointer,
      RemoteCursor.text,
      RemoteCursor.notAllowed,
      RemoteCursor.resizeEW,
      RemoteCursor.resizeNS,
      RemoteCursor.move,
      RemoteCursor.grab,
      RemoteCursor.grabbing,
    ];
    for (final (index, shape) in shapes.indexed) {
      expect((DesktopMeta.decode([0x9b, index]) as CursorUpdate).shape, shape);
    }
    expect(
      (DesktopMeta.decode([0x9b, 255]) as CursorUpdate).shape,
      RemoteCursor.arrow,
    );
  });
}
