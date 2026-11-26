import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../activity_history.dart';
import '../lan_remote_session.dart';
import '../lan_session.dart';
import '../mobile_services.dart';
import 'biometric_key.dart';
import 'protocol.dart';
import 'transport.dart';

class PairCredentials {
  PairCredentials([this.storage = const FlutterSecureStorage()]);
  final FlutterSecureStorage storage;
  Future<SimpleKeyPair>? _key;

  Future<SimpleKeyPair> key() async {
    try {
      return await (_key ??= _loadKey());
    } catch (_) {
      _key = null;
      rethrow;
    }
  }

  Future<SimpleKeyPair> _loadKey() async {
    final existing = await storage.read(key: 'pairing.seed.v1');
    final seed = existing == null
        ? randomPairBytes(32)
        : decodePairBytes(existing, 32);
    if (existing == null) {
      await storage.write(key: 'pairing.seed.v1', value: encodePairBytes(seed));
    }
    return Ed25519().newKeyPairFromSeed(seed);
  }
}

class PairedComputer {
  const PairedComputer(this.computer, this.target, {this.customName = false});
  final Computer computer;
  final PairTarget target;
  final bool customName;

  factory PairedComputer.fromJson(PairMessage json) => PairedComputer(
    Computer.fromJson(json['computer'] as PairMessage),
    PairTarget.fromJson(json['target'] as PairMessage),
    customName: json['customName'] == true,
  );

  PairMessage toJson() => {
    'computer': computer.toJson(),
    'target': target.toJson(),
    if (customName) 'customName': true,
  };
}

class PairNetwork {
  const PairNetwork(this.credentials);
  final PairCredentials credentials;

  Future<T> use<T>(
    PairTarget target,
    String purpose,
    Future<T> Function(AuthenticatedPair) run,
  ) async {
    final key = await credentials.key();
    Object? lastError;
    PairTrustFailure? trustFailure;
    Future<T> attempt(Future<PairTransport> Function() open) async {
      final transport = await open();
      try {
        final pair = await AuthenticatedPair.start(
          target,
          transport,
          purpose,
          key,
        );
        return await run(pair);
      } finally {
        await transport.close();
      }
    }

    for (final address in target.lan) {
      try {
        return await attempt(() => PairTransport.lan(address));
      } on PairTrustFailure catch (error) {
        trustFailure = error;
      } on SocketException catch (error) {
        lastError = error;
      } on TimeoutException catch (error) {
        lastError = error;
      }
    }
    if (target.server != null) {
      return attempt(() => PairTransport.server(target, purpose));
    }
    if (trustFailure != null) throw trustFailure;
    if (lastError != null) throw const PairFailure('无法连接电脑，请检查局域网连接');
    throw const PairFailure('电脑没有可用的连接地址');
  }

  Future<AuthenticatedPair> begin(PairTarget target) async {
    final key = await credentials.key();
    Future<AuthenticatedPair> open(
      Future<PairTransport> Function() connect,
    ) async {
      final transport = await connect();
      try {
        return await AuthenticatedPair.start(
          target,
          transport,
          target.manual ? 'pair_manual' : 'pair',
          key,
        );
      } catch (_) {
        await transport.close();
        rethrow;
      }
    }

    PairTrustFailure? trustFailure;
    for (final address in target.lan) {
      try {
        return await open(() => PairTransport.lan(address));
      } on PairTrustFailure catch (error) {
        trustFailure = error;
      } on SocketException {
        continue;
      } on TimeoutException {
        continue;
      }
    }
    if (target.server != null) {
      return open(() => PairTransport.server(target, 'pair'));
    }
    throw trustFailure ?? const PairFailure('无法连接电脑，请检查局域网连接');
  }
}

class PairedDevices extends ChangeNotifier implements DeviceRepository {
  PairedDevices(this.network);
  static const _storageKey = 'pairing.computers.v1';
  final PairNetwork network;
  final _records = <String, PairedComputer>{};
  final _presence =
      <String, ({int? latency, bool relay, bool? bypass, bool revoked})>{};
  Future<void> _writes = Future.value();
  bool _refreshing = false;
  bool _disposed = false;
  Timer? _poll;

  Future<void> load({bool poll = true}) async {
    final stored = await network.credentials.storage.read(key: _storageKey);
    if (stored != null) {
      for (final json in jsonDecode(stored) as List) {
        final record = PairedComputer.fromJson(json as PairMessage);
        _records[record.computer.id] = record;
      }
    }
    if (poll) {
      _poll = Timer.periodic(
        const Duration(seconds: 10),
        (_) => unawaited(refresh()),
      );
      unawaited(refresh());
    }
  }

  @override
  List<Computer> get computers => List.unmodifiable(
    _records.entries.map((entry) {
      final c = entry.value.computer, live = _presence[entry.key];
      return Computer(
        id: c.id,
        name: c.name,
        os: c.os,
        path: live?.relay == true
            ? ConnectionPath.relay
            : ConnectionPath.direct,
        latency: live?.latency,
        confirmOnHost: !(live?.bypass ?? !c.confirmOnHost),
        lastSeen: live?.revoked == true ? '已解除配对' : null,
      );
    }),
  );

  PairTarget? target(String id) => _records[id]?.target;

  Computer? matchingComputer(PairTarget target) {
    final saved = _records[target.id];
    if (saved == null ||
        saved.target.fingerprint != target.fingerprint ||
        _presence[target.id]?.revoked == true) {
      return null;
    }
    return computers.firstWhere((computer) => computer.id == target.id);
  }

  Future<void> _updateFromScan(
    PairTarget previous,
    PairTarget scanned,
    String? name,
  ) async {
    final sameRoute =
        previous.server == scanned.server &&
        previous.token == scanned.token &&
        previous.serverKey == scanned.serverKey &&
        listEquals(previous.lan, scanned.lan);
    await _change((records) {
      final current = records[previous.id];
      if (current == null || current.target != previous) return;
      final nextName = !current.customName && name != null && name.isNotEmpty
          ? name
          : current.computer.name;
      if (sameRoute && nextName == current.computer.name) return;
      records[previous.id] = PairedComputer(
        current.computer.renamed(nextName),
        sameRoute ? previous : scanned,
        customName: current.customName,
      );
    });
  }

  Future<void> savePair(Computer computer, PairTarget target) =>
      _change((records) {
        records[computer.id] = PairedComputer(computer, target);
      });

  @override
  Future<void> add(Computer computer) async {
    // PairingService saves the record with the verified network credentials.
  }

  @override
  Future<void> rename(String id, String name) => _change((records) {
    final record = records[id];
    if (record != null && name.trim().isNotEmpty) {
      records[id] = PairedComputer(
        record.computer.renamed(name.trim()),
        record.target,
        customName: true,
      );
    }
  });

  @override
  Future<UnpairResult> remove(String id) async {
    final record = _records[id];
    if (record == null) return UnpairResult.removed;
    var result = UnpairResult.removed;
    if (_presence[id]?.revoked != true) {
      try {
        await network.use(record.target, 'unpair', (pair) async {
          await pair.send({'type': 'request'});
          final reply = await pair.receive();
          if (reply['status'] != 'ok') throw const PairFailure('电脑无法解除配对');
        });
      } on PairUnpaired {
        // The computer already revoked this phone.
      } catch (_) {
        result = UnpairResult.localOnly;
      }
    }
    await _change((records) async {
      if (records[id]?.target != record.target) return;
      // The Ed25519 identity is shared by all computers. Only the P-256 key
      // and the connection credentials belong to this pairing.
      await const BiometricKey().remove(id);
      records.remove(id);
    });
    return result;
  }

  Future<void> _change(
    FutureOr<void> Function(Map<String, PairedComputer>) change,
  ) {
    final write = _writes.then((_) async {
      final next = Map.of(_records);
      await change(next);
      if (mapEquals(next, _records)) return;
      await network.credentials.storage.write(
        key: _storageKey,
        value: jsonEncode(next.values.toList()),
      );
      _presence.removeWhere(
        (id, _) => next[id]?.target != _records[id]?.target,
      );
      _records
        ..clear()
        ..addAll(next);
      if (!_disposed) notifyListeners();
    });
    _writes = write.catchError((Object _) {});
    return write;
  }

  Future<void> refresh() async {
    if (_refreshing || _disposed) return;
    _refreshing = true;
    try {
      await Future.wait(
        _records.values.toList().map((record) async {
          final id = record.computer.id;
          final watch = Stopwatch()..start();
          try {
            final live = await network.use(record.target, 'status', (
              pair,
            ) async {
              await pair.send({'type': 'request'});
              final reply = await pair.receive();
              if (reply['status'] != 'ok') throw const PairFailure('电脑状态未知');
              return (
                latency: watch.elapsedMilliseconds,
                relay: pair.transport.relay,
                bypass: reply['bypass'] == true,
                revoked: false,
              );
            });
            if (_records[id]?.target == record.target) _presence[id] = live;
          } on PairUnpaired {
            if (_records[id]?.target == record.target) markRevoked(id);
          } on Exception {
            final previous = _presence[id];
            if (_records[id]?.target == record.target &&
                previous != null &&
                !previous.revoked) {
              _presence[id] = (
                latency: null,
                relay: false,
                bypass: previous.bypass,
                revoked: false,
              );
            }
          }
        }),
      );
    } finally {
      _refreshing = false;
      if (!_disposed) notifyListeners();
    }
  }

  void markRevoked(String id) {
    _presence[id] = (latency: null, relay: false, bypass: null, revoked: true);
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _poll?.cancel();
    super.dispose();
  }
}

class RealPairingService implements PairingService {
  RealPairingService(this.devices, {this.phoneInfo = _phoneInfo});
  final PairedDevices devices;
  final Future<({String name, String model})> Function() phoneInfo;

  @override
  Future<PairRequest> begin(String input, {String? server}) async {
    final target = PairTarget.parse(input);
    final saved = devices.matchingComputer(target);
    if (saved != null) {
      final previous = devices.target(saved.id)!;
      unawaited(_refreshFromScan(previous, target));
      return PairRequest(
        computer: saved,
        server: target.server,
        verificationCode: '',
        statuses: Stream.value(PairStatus.alreadyPaired),
        cancel: () {},
        initialStatus: PairStatus.alreadyPaired,
      );
    }
    final phone = await phoneInfo();
    final pair = await devices.network.begin(target);
    final challenge = pair.challenge;
    final name = challenge['name'] as String?;
    final verifiedTarget = target.verified(pair.transcript.hostKey);
    final computer = Computer(
      id: verifiedTarget.id,
      name: name == null || name.isEmpty ? 'kkdesk 电脑' : name,
      os: switch (challenge['os']) {
        'darwin' => ComputerOS.mac,
        'windows' => ComputerOS.windows,
        'linux' => ComputerOS.linux,
        _ => ComputerOS.unknown,
      },
    );
    final code = verificationCode(
      target.code,
      decodePairBytes(pair.transcript.hostKey, 32),
      decodePairBytes(pair.transcript.clientKey, 32),
    );
    final states = StreamController<PairStatus>();
    var cancelled = false;
    unawaited(() async {
      try {
        await pair.send({
          'type': 'request',
          'code': pair.transcript.codeProof(target.code),
          'name': phone.name,
          'model': phone.model,
        });
        var reply = await pair.receive();
        if (reply['status'] == 'pending') {
          if (reply['code'] != code) throw const PairTrustFailure('核对码不一致');
          if (!cancelled) states.add(PairStatus.waiting);
          reply = await pair.receive(const Duration(seconds: 35));
        }
        if (cancelled) return;
        String? approval;
        if (reply['status'] == 'approved') {
          approval = reply['nonce'] as String;
          decodePairBytes(approval, 32);
          await pair.send({'type': 'pair_ack', 'nonce': approval});
          reply = await pair.receive();
        }
        if (cancelled) return;
        if (reply['status'] == 'ok') {
          if (approval == null || reply['nonce'] != approval) {
            throw const PairTrustFailure('电脑配对确认无效');
          }
          await pair.send({'type': 'pair_received', 'nonce': approval});
          reply = await pair.receive();
        }
        if (cancelled) return;
        final status = switch (reply['status']) {
          'committed' => PairStatus.success,
          'already_paired'
              when devices.matchingComputer(verifiedTarget) != null =>
            PairStatus.alreadyPaired,
          'already_paired' => throw const PairFailure(
            '电脑仍保留这台手机的旧配对，请先在电脑端「已配对手机」中移除后再扫码',
          ),
          'busy' => throw const PairFailure('电脑正在处理另一个配对请求，请稍后再试'),
          'invalid' => throw const PairFailure('手机信息无效'),
          'rejected' => PairStatus.rejected,
          'expired' => PairStatus.expired,
          'timed_out' => PairStatus.timedOut,
          final status => throw PairFailure('配对失败（${status ?? '无状态码'}）'),
        };
        if (status == PairStatus.success) {
          if (approval == null || reply['nonce'] != approval) {
            throw const PairTrustFailure('电脑配对确认无效');
          }
          final record = Computer(
            id: computer.id,
            name: computer.name,
            os: computer.os,
            confirmOnHost: reply['bypass'] != true,
          );
          await devices.savePair(record, verifiedTarget);
          unawaited(devices.refresh());
        } else if (status == PairStatus.alreadyPaired) {
          final previous = devices.target(computer.id)!;
          await devices._updateFromScan(
            previous,
            target.manual ? previous : verifiedTarget,
            name,
          );
        }
        if (!cancelled) states.add(status);
      } on TimeoutException {
        if (!cancelled) states.add(PairStatus.timedOut);
      } catch (error, trace) {
        if (!cancelled) states.addError(error, trace);
      } finally {
        await pair.transport.close();
        unawaited(states.close());
      }
    }());
    return PairRequest(
      computer: computer,
      server: target.server,
      verificationCode: code,
      statuses: states.stream,
      cancel: () {
        cancelled = true;
        unawaited(pair.transport.close());
      },
    );
  }

  Future<void> _refreshFromScan(PairTarget previous, PairTarget scanned) async {
    try {
      // The desktop sends its signed name before receiving a pairing request.
      // Close after the challenge so the invitation is never claimed.
      final pair = await devices.network.begin(scanned);
      try {
        await devices._updateFromScan(
          previous,
          scanned.verified(pair.transcript.hostKey),
          pair.challenge['name'] as String?,
        );
      } finally {
        await pair.transport.close();
      }
    } catch (_) {
      // A known pairing can still be shown when the desktop is unreachable.
    }
  }
}

Future<({String name, String model})> _phoneInfo() async {
  final info = DeviceInfoPlugin();
  try {
    if (Platform.isIOS) {
      final device = await info.iosInfo;
      return (name: device.name, model: device.modelName);
    }
    if (Platform.isAndroid) {
      final device = await info.androidInfo;
      return (name: device.model, model: device.manufacturer);
    }
  } on Exception {
    // Pairing is still available when the platform doesn't supply a name.
  }
  return (name: Platform.isIOS ? 'iPhone' : 'Android 手机', model: '');
}

class PairedRemoteSessions implements RemoteSessions {
  PairedRemoteSessions(
    this.devices, {
    this.biometricKey = const BiometricKey(),
  });
  final PairedDevices devices;
  final BiometricKey biometricKey;
  final notices = ValueNotifier<String?>(null);

  @override
  RemoteSession paired(Computer computer) =>
      LanRemoteSession.paired(computer.name, connection(computer));

  PairedConnection connection(Computer computer) => (run) async {
    final target = devices.target(computer.id);
    if (target == null || computer.lastSeen == '已解除配对') {
      throw const PairUnpaired();
    }
    try {
      await devices.network.use(target, 'connect', (pair) async {
        if (pair.bypass) {
          pair.protectOffer = (offer) =>
              _authorizeOffer(computer.id, pair, offer);
        }
        await run(pair);
      });
    } on PairUnpaired {
      devices.markRevoked(computer.id);
      rethrow;
    }
  };

  Future<PairMessage> _authorizeOffer(
    String id,
    AuthenticatedPair pair,
    PairMessage offer,
  ) async {
    final registered = pair.challenge['biometricKey'];
    try {
      final publicKey = encodePairBytes(await biometricKey.prepare(id));
      final payload = {...offer, 'biometricKey': publicKey};
      if (publicKey != registered) {
        notices.value = registered == null
            ? '完成生物识别后，请在电脑上确认登记免确认密钥。'
            : '生物识别密钥已更换，请在电脑上确认重新开启免确认。';
      }
      final signature = await biometricKey.sign(
        id,
        pair.transcript.bytes(payload),
      );
      return {...payload, 'biometricSignature': encodePairBytes(signature)};
    } on PlatformException catch (error) {
      if (error.code == 'invalidated' || error.code == 'missing') {
        try {
          await biometricKey.remove(id);
        } on PlatformException {
          // The desktop can still confirm this connection.
        }
        notices.value = '生物识别密钥已失效，本次由电脑确认。下次连接时重新开启免确认。';
      } else {
        notices.value = '本次连接需要在电脑上确认。';
      }
      return offer;
    }
  }

  @override
  RemoteSession lan(String address, String password) =>
      LanRemoteSession(address, password);
}

Future<MobileServices> realMobileServices(SharedPreferences prefs) async {
  final network = PairNetwork(PairCredentials());
  final devices = PairedDevices(network);
  await devices.load();
  final bio = DeviceBiometrics();
  return MobileServices(
    devices: devices,
    pairing: RealPairingService(devices),
    sessions: PairedRemoteSessions(devices),
    settings: MobileSettings(prefs),
    biometrics: bio,
    history: ActivityHistory(prefs),
  );
}
