import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../activity_history.dart';
import '../lan_remote_session.dart';
import '../lan_session.dart';
import '../mobile_services.dart';
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
  const PairedComputer(this.computer, this.target);
  final Computer computer;
  final PairTarget target;

  factory PairedComputer.fromJson(PairMessage json) => PairedComputer(
    Computer.fromJson(json['computer'] as PairMessage),
    PairTarget.fromJson(json['target'] as PairMessage),
  );

  PairMessage toJson() => {
    'computer': computer.toJson(),
    'target': target.toJson(),
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
        return await AuthenticatedPair.start(target, transport, 'pair', key);
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
      );
    }
  });

  @override
  Future<void> remove(String id) async {
    final record = _records[id];
    if (record == null) return;
    if (_presence[id]?.revoked != true) {
      try {
        await network.use(record.target, 'unpair', (pair) async {
          await pair.send({'type': 'request'});
          final reply = await pair.receive();
          if (reply['status'] != 'ok') throw const PairFailure('电脑无法解除配对');
        });
      } on PairUnpaired {
        // The computer already revoked this phone.
      }
    }
    await _change((records) => records.remove(id));
  }

  Future<void> _change(void Function(Map<String, PairedComputer>) change) {
    final write = _writes.then((_) async {
      final next = Map.of(_records);
      change(next);
      await network.credentials.storage.write(
        key: _storageKey,
        value: jsonEncode(next.values.toList()),
      );
      _presence.removeWhere((id, _) => next[id] != _records[id]);
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
            if (_records[id]?.target == record.target &&
                _presence[id]?.revoked != true) {
              _presence.remove(id);
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
    final phone = await phoneInfo();
    final pair = await devices.network.begin(target);
    final challenge = pair.challenge;
    final name = challenge['name'] as String?;
    final computer = Computer(
      id: target.id,
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
        final status = switch (reply['status']) {
          'ok' => PairStatus.success,
          'expired' => PairStatus.expired,
          'timed_out' => PairStatus.timedOut,
          _ => PairStatus.rejected,
        };
        if (status == PairStatus.success) {
          final record = Computer(
            id: computer.id,
            name: computer.name,
            os: computer.os,
            confirmOnHost: reply['bypass'] != true,
          );
          await devices.savePair(
            record,
            target.verified(pair.transcript.hostKey),
          );
          unawaited(devices.refresh());
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
  PairedRemoteSessions(this.devices, this.biometrics);
  final PairedDevices devices;
  final Biometrics biometrics;

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
        if (pair.bypass &&
            !await biometrics.authenticate('验证身份以连接「${computer.name}」')) {
          throw const PairFailure('生物识别未通过');
        }
        await run(pair);
      });
    } on PairUnpaired {
      devices.markRevoked(computer.id);
      rethrow;
    }
  };

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
    sessions: PairedRemoteSessions(devices, bio),
    settings: MobileSettings(prefs),
    biometrics: bio,
    history: ActivityHistory(prefs),
  );
}
