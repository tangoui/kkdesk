import 'dart:convert';
import 'dart:typed_data';

enum PrivacyReplyKind { screen, lockAfterDisconnect, lockNow }

enum PrivacyStatus {
  off,
  on,
  waiting,
  denied,
  notAllowed,
  unsupported,
  localExit,
  failed,
}

enum LockScreenState {
  unknown,
  unlocked,
  ready,
  componentUnavailable,
  platformUnsupported,
  signatureRequired,
  wrongInstallLocation,
  serviceUnavailable,
  componentNeedsUpdate,
  componentError,
  approvalRequired,
}

enum UnlockResult { succeeded, stillLocked, unsupported, tooMany }

enum RemoteCursor {
  arrow,
  pointer,
  text,
  notAllowed,
  resizeEW,
  resizeNS,
  move,
  grab,
  grabbing,
}

sealed class DesktopMeta {
  const DesktopMeta();

  // The controller accepts only host-to-controller messages.
  static DesktopMeta? decode(List<int> bytes) {
    if (bytes.length < 2) return null;
    final code = bytes[1];
    switch (bytes[0]) {
      case 0x91:
        if (code < 0 || code > 6) return null;
        return PrivacyReply.decode(PrivacyReplyKind.screen, bytes);
      case 0x93:
        if (code != 0 && code != 1 && code != 5) return null;
        return PrivacyReply.decode(PrivacyReplyKind.lockAfterDisconnect, bytes);
      case 0x95:
        if (code != 1 && code != 5 && code != 7) return null;
        return PrivacyReply.decode(PrivacyReplyKind.lockNow, bytes);
    }
    if (bytes.length != 2) return null;
    return switch (bytes[0]) {
      0x98 when code >= 1 && code < LockScreenState.values.length =>
        LockScreenUpdate(LockScreenState.values[code]),
      0x9a when code >= 1 && code <= UnlockResult.values.length => UnlockReply(
        UnlockResult.values[code - 1],
      ),
      0x9b => CursorUpdate(
        code >= 0 && code < RemoteCursor.values.length
            ? RemoteCursor.values[code]
            : RemoteCursor.arrow,
      ),
      _ => null,
    };
  }
}

final class PrivacyReply extends DesktopMeta {
  const PrivacyReply(this.kind, this.status, this.detail);
  final PrivacyReplyKind kind;
  final PrivacyStatus status;
  final String detail;

  static PrivacyReply? decode(PrivacyReplyKind kind, List<int> bytes) {
    final status = PrivacyStatus.values[bytes[1]];
    if (bytes.length == 2) return PrivacyReply(kind, status, '');
    if (bytes.length > 2050 ||
        (status != PrivacyStatus.unsupported &&
            status != PrivacyStatus.failed)) {
      return null;
    }
    try {
      return PrivacyReply(kind, status, const Utf8Decoder().convert(bytes, 2));
    } on FormatException {
      return null;
    }
  }
}

final class LockScreenUpdate extends DesktopMeta {
  const LockScreenUpdate(this.state);
  final LockScreenState state;
}

final class UnlockReply extends DesktopMeta {
  const UnlockReply(this.result);
  final UnlockResult result;
}

final class CursorUpdate extends DesktopMeta {
  const CursorUpdate(this.shape);
  final RemoteCursor shape;
}

Uint8List encodePrivacyRequest(bool on) =>
    Uint8List.fromList([0x90, on ? 1 : 0]);

Uint8List encodeLockAfterDisconnectRequest(bool on) =>
    Uint8List.fromList([0x92, on ? 1 : 0]);

Uint8List encodeLockNowRequest() => Uint8List.fromList([0x94]);

Uint8List? encodeUnlockRequest(Uint8List secret) {
  if (secret.isEmpty || secret.length > 1024 || secret.contains(0)) return null;
  try {
    utf8.decode(secret);
  } on FormatException {
    return null;
  }
  return Uint8List(secret.length + 1)
    ..[0] = 0x99
    ..setRange(1, secret.length + 1, secret);
}
