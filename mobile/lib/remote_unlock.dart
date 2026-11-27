import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:local_auth/local_auth.dart';

import 'desktop_meta.dart';
import 'pairing/protocol.dart';

enum SavedUnlock { manual, finished }

class RemoteUnlock {
  RemoteUnlock({
    required this._hostKey,
    required this._canSend,
    required this._send,
    required this._changed,
    LocalAuthentication? auth,
    this._storage = const FlutterSecureStorage(),
  }) : _auth = auth ?? LocalAuthentication();

  final String? Function() _hostKey;
  final bool Function() _canSend;
  final Future<bool> Function(Uint8List) _send;
  final void Function() _changed;
  final LocalAuthentication _auth;
  final FlutterSecureStorage _storage;

  bool _closed = false;
  bool _working = false;
  bool _saving = false;
  bool _authenticating = false;
  _UnlockAttempt? _attempt;
  String? _message;
  bool _savedFailed = false;

  bool get busy => _working || _saving || _attempt != null;
  String? get message => _message;
  bool get savedFailed => _savedFailed;
  bool get _ready => !_closed && _canSend();

  String? get _account {
    final host = _hostKey();
    if (host == null) return null;
    try {
      return 'remote.unlock.v1.${fingerprint(decodePairBytes(host, 32))}';
    } on FormatException {
      return null;
    }
  }

  Future<bool?> _biometricOnly() async {
    try {
      if ((await _auth.getAvailableBiometrics()).isNotEmpty) return true;
      if (await _auth.isDeviceSupported()) return false;
    } on Exception {
      return null;
    }
    return null;
  }

  Future<bool> canRemember() async =>
      !_closed && _account != null && await _biometricOnly() != null;

  Future<bool> hasSaved() async {
    final account = _account;
    if (_closed || account == null) return false;
    try {
      return await _storage.containsKey(key: account);
    } on Exception {
      return false;
    }
  }

  Future<SavedUnlock> requestSaved() async {
    if (!_ready || busy) return SavedUnlock.finished;
    final account = _account;
    if (account == null) return SavedUnlock.manual;
    _working = true;
    _message = null;
    _notify();
    try {
      if (!await _storage.containsKey(key: account)) return SavedUnlock.manual;
      final biometricOnly = await _biometricOnly();
      if (!_ready || _account != account) return SavedUnlock.finished;
      if (biometricOnly == null) return SavedUnlock.manual;
      if (!await _authenticate(biometricOnly) ||
          !_ready ||
          _account != account) {
        return SavedUnlock.finished;
      }
      final stored = await _storage.read(key: account);
      if (!_ready || _account != account) return SavedUnlock.finished;
      if (stored == null) return SavedUnlock.manual;
      final secret = Uint8List.fromList(utf8.encode(stored));
      try {
        await _submit(secret, saved: true);
      } finally {
        _clear(secret);
      }
    } on Exception {
      _message = '无法读取本机保存的解锁密码';
    } finally {
      _working = false;
      _notify();
    }
    return SavedUnlock.finished;
  }

  Future<bool> _authenticate(bool biometricOnly) async {
    _authenticating = true;
    try {
      return await _auth.authenticate(
        localizedReason: '验证身份以使用已保存的解锁密码',
        biometricOnly: biometricOnly,
      );
    } on Exception {
      return false;
    } finally {
      _authenticating = false;
    }
  }

  Future<void> submit(Uint8List secret, {required bool remember}) async {
    if (!_ready || busy) return;
    _working = true;
    _notify();
    try {
      final account = remember && await canRemember() ? _account : null;
      if (_ready) await _submit(secret, account: account);
    } finally {
      _working = false;
      _notify();
    }
  }

  Future<void> _submit(
    Uint8List secret, {
    String? account,
    bool saved = false,
  }) async {
    final attempt = _UnlockAttempt(
      saved,
      account,
      account == null ? null : Uint8List.fromList(secret),
    );
    _message = null;
    _savedFailed = false;
    _attempt = attempt;
    attempt.timer = Timer(const Duration(seconds: 25), () {
      attempt.clearSecret();
      _message = '等待解锁结果超时';
      _notify();
    });
    try {
      if (await _send(secret)) return;
    } on Exception {
      // The transport can disappear while a request is being sent.
    }
    if (_attempt != attempt) return;
    _clearAttempt();
    _message = '无法发送解锁请求';
  }

  Future<void> receive(UnlockResult result) async {
    final attempt = _attempt;
    if (_closed || attempt == null) return;
    final bytes = attempt.secret;
    attempt.secret = null;
    _clearAttempt();
    _savedFailed = result == UnlockResult.stillLocked && attempt.saved;
    _message = switch (result) {
      UnlockResult.succeeded => '对方电脑已解锁',
      UnlockResult.stillLocked => _savedFailed ? '已保存的解锁密码不正确' : '解锁密码可能不正确',
      UnlockResult.unsupported => '对方电脑当前无法远程解锁',
      UnlockResult.tooMany => '本次连接的解锁尝试次数已达上限',
    };
    if (bytes != null && result == UnlockResult.succeeded) {
      _saving = true;
      try {
        final value = utf8.decode(bytes);
        _clear(bytes);
        await _storage.write(key: attempt.account!, value: value);
      } on Exception {
        _message = '已解锁，无法保存密码';
      } finally {
        _saving = false;
      }
    }
    if (bytes != null) _clear(bytes);
    _notify();
  }

  Future<void> forget() async {
    final account = _account;
    if (_closed || busy || account == null) return;
    _working = true;
    _notify();
    try {
      await _storage.delete(key: account);
      _savedFailed = false;
      _message = null;
    } on Exception {
      _message = '无法删除本机保存的解锁密码';
    } finally {
      _working = false;
      _notify();
    }
  }

  void close() {
    _closed = true;
    _clearAttempt();
    if (_authenticating) unawaited(_stopAuthentication());
  }

  Future<void> _stopAuthentication() async {
    try {
      await _auth.stopAuthentication();
    } on Exception {
      // Any result arriving after close is ignored.
    }
  }

  void _clearAttempt() {
    _attempt?.timer?.cancel();
    _attempt?.clearSecret();
    _attempt = null;
  }

  void _notify() {
    if (!_closed) _changed();
  }
}

void _clear(Uint8List bytes) => bytes.fillRange(0, bytes.length, 0);

class _UnlockAttempt {
  _UnlockAttempt(this.saved, this.account, this.secret);

  final bool saved;
  final String? account;
  Uint8List? secret;
  Timer? timer;

  void clearSecret() {
    if (secret case final bytes?) _clear(bytes);
    secret = null;
  }
}
