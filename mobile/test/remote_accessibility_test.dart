import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kkdesk_mobile/lan_remote_session.dart';
import 'package:kkdesk_mobile/mobile_services.dart';
import 'package:kkdesk_mobile/pairing/protocol.dart';
import 'package:kkdesk_mobile/pairing/transport.dart';
import 'package:kkdesk_mobile/ui/files/transfer_center.dart';
import 'package:kkdesk_mobile/ui/kit/overlays.dart';
import 'package:kkdesk_mobile/ui/remote.dart';
import 'package:shared_preferences_platform_interface/in_memory_shared_preferences_async.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_async_platform_interface.dart';

const _warning = '对方未允许键盘鼠标控制，当前只能查看';

void main() {
  testWidgets('kit notice displays its warning and offers dismissal', (
    tester,
  ) async {
    var dismissed = false;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: KNoticeBar(
            title: _warning,
            kind: KNoticeKind.warning,
            onDismiss: () => dismissed = true,
          ),
        ),
      ),
    );
    expect(find.text(_warning), findsOneWidget);
    await tester.tap(find.byTooltip('关闭提示'));
    expect(dismissed, isTrue);
  });

  testWidgets('paired remote page follows accessibility messages on meta', (
    tester,
  ) async {
    final preferences = SharedPreferencesAsyncPlatform.instance;
    SharedPreferencesAsyncPlatform.instance =
        InMemorySharedPreferencesAsync.empty();
    addTearDown(() => SharedPreferencesAsyncPlatform.instance = preferences);
    final webRTC = _WebRTC(tester);
    final shot = Platform.environment['KKDESK_MOBILE_ACCESSIBILITY_SHOT'];
    ui.Image? screenImage;
    if (shot != null) {
      await tester.runAsync(() async {
        final font = FontLoader('Screenshot Chinese')
          ..addFont(
            Future.value(
              ByteData.sublistView(
                File(Platform.environment['KKDESK_SCREENSHOT_FONT']!)
                    .readAsBytesSync(),
              ),
            ),
          );
        final codec = await ui.instantiateImageCodec(
          File('assets/demo_desktop.jpg').readAsBytesSync(),
        );
        screenImage = (await codec.getNextFrame()).image;
        codec.dispose();
        final icons = FontLoader('packages/cupertino_icons/CupertinoIcons')
          ..addFont(
            rootBundle.load(
              'packages/cupertino_icons/assets/CupertinoIcons.ttf',
            ),
          );
        await font.load();
        await icons.load();
      });
    }

    await tester.binding.setSurfaceSize(const Size(844, 390));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final services = MobileServices.demo();
    await services.settings.seeGestures();
    final session = _PreviewSession(screenImage);
    final files = TransferCenter();
    final window = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: window,
        child: MaterialApp(
          debugShowCheckedModeBanner: false,
          theme: ThemeData(
            fontFamily: shot == null ? null : 'Screenshot Chinese',
          ),
          home: RemotePage(
            computer: const Computer(
              id: 'mac',
              name: 'Mac',
              os: ComputerOS.mac,
            ),
            session: session,
            settings: services.settings,
            history: services.history,
            fileCenter: files,
          ),
        ),
      ),
    );
    if (shot != null) {
      final context = tester.element(find.byType(RemotePage));
      await tester.runAsync(
        () =>
            precacheImage(const AssetImage('assets/kkdesk_mark.png'), context),
      );
    }
    await tester.pump();
    expect(webRTC.calls, contains('setRemoteDescription'));
    expect(
      webRTC.listeners,
      contains('FlutterWebRTC/dataChannelEventpeermeta'),
    );
    await webRTC.event('peerConnectionEventpeer', {
      'event': 'peerConnectionState',
      'state': 'connected',
    });
    await tester.pump();

    // Permission can arrive before the first video frame.
    await webRTC.permission('denied');
    await tester.pump();
    expect(
      session.status.accessibilityDenied,
      isTrue,
      reason: session.status.detail,
    );
    expect(find.text(_warning), findsNothing);
    await webRTC.event('Texture42', {'event': 'didFirstFrameRendered'});
    await tester.pump();
    expect(
      session.status.phase,
      RemotePhase.connected,
      reason: session.status.detail,
    );
    expect(find.text(_warning), findsOneWidget);
    expect(find.byKey(const ValueKey('trackpad-cursor')), findsNothing);
    if (shot != null) {
      await tester.runAsync(() async {
        final boundary =
            window.currentContext!.findRenderObject()! as RenderRepaintBoundary;
        final image = await boundary.toImage(pixelRatio: 2);
        final png = (await image.toByteData(format: ui.ImageByteFormat.png))!;
        File(shot).writeAsBytesSync(png.buffer.asUint8List());
        image.dispose();
      });
    }

    await tester.tap(find.byTooltip('关闭提示'));
    await tester.pump();
    expect(find.text(_warning), findsNothing);
    await tester.tap(find.byKey(const ValueKey('remote-ball')));
    await tester.pumpAndSettle();
    expect(find.text(_warning), findsOneWidget);

    await webRTC.permission('granted');
    await tester.pump();
    expect(find.text(_warning), findsNothing);
    expect(find.byKey(const ValueKey('trackpad-cursor')), findsOneWidget);
    await tester.tapAt(const Offset(20, 20));
    await tester.pumpAndSettle();
    await webRTC.permission('denied');
    await tester.pump();
    expect(find.byType(KNoticeBar), findsOneWidget);
    await webRTC.meta(Uint8List.fromList([0x62]));
    await tester.pump();
    expect(session.status.phase, RemotePhase.ended);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    files.dispose();
    screenImage?.dispose();
  });
}

class _PreviewSession extends LanRemoteSession {
  _PreviewSession(this.image)
    : super.paired('Mac', (connect) => connect(_Pair()));

  final ui.Image? image;

  @override
  Widget video() => RawImage(image: image, fit: BoxFit.contain);
}

class _Pair implements AuthenticatedPair {
  @override
  final transcript = const PairTranscript(
    purpose: 'connect',
    clientNonce: '',
    hostNonce: '',
    clientKey: '',
    hostKey: '',
  );

  @override
  final PairTransport transport = _Transport();

  @override
  Future<void> send(PairMessage msg) async {}

  @override
  Future<PairMessage> receive([
    Duration timeout = const Duration(seconds: 8),
  ]) async => {'type': 'answer', 'sdp': 'answer'};

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Transport implements PairTransport {
  @override
  bool get relay => false;

  @override
  bool get relayOnly => false;

  @override
  List<PairMessage> get ice => const [];

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _WebRTC {
  _WebRTC(WidgetTester tester)
    : messenger = tester.binding.defaultBinaryMessenger {
    const method = MethodChannel('FlutterWebRTC.Method');
    messenger.setMockMethodCallHandler(method, (call) async {
      calls.add(call.method);
      switch (call.method) {
        case 'createVideoRenderer':
          return {'textureId': 42};
        case 'createPeerConnection':
          return {'peerConnectionId': 'peer'};
        case 'createDataChannel':
          return {'id': 1, 'flutterId': call.arguments['label']};
        case 'addTransceiver':
          const parameters = {
            'encodings': [],
            'headerExtensions': [],
            'codecs': [],
            'rtcp': {'reducedSize': false},
          };
          return {
            'direction': 'recvonly',
            'sender': {
              'senderId': 'sender',
              'ownsTrack': false,
              'track': <String, Object>{},
              'rtpParameters': parameters,
            },
            'receiver': {
              'receiverId': 'receiver',
              'track': {
                'id': 'video',
                'label': 'video',
                'kind': 'video',
                'enabled': true,
              },
              'rtpParameters': parameters,
            },
          };
        case 'createOffer':
        case 'getLocalDescription':
          return {'type': 'offer', 'sdp': 'offer'};
        case 'setLocalDescription':
          await event('peerConnectionEventpeer', {
            'event': 'iceGatheringState',
            'state': 'complete',
          });
          return null;
        default:
          return null;
      }
    });
    addTearDown(() => messenger.setMockMethodCallHandler(method, null));
    for (final name in [
      'FlutterWebRTC.Event',
      'FlutterWebRTC/Texture42',
      'FlutterWebRTC/peerConnectionEventpeer',
      for (final label in ['input-move', 'input', 'meta', 'files'])
        'FlutterWebRTC/dataChannelEventpeer$label',
    ]) {
      final channel = MethodChannel(name);
      messenger.setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'listen') listeners.add(name);
        return null;
      });
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    }
  }

  final TestDefaultBinaryMessenger messenger;
  final calls = <String>[];
  final listeners = <String>[];

  Future<void> event(String stream, Map<String, Object> data) async {
    await messenger.handlePlatformMessage(
      'FlutterWebRTC/$stream',
      const StandardMethodCodec().encodeSuccessEnvelope(data),
      null,
    );
  }

  Future<void> permission(String state) => meta(
    File('test/fixtures/meta/accessibility-$state.bin').readAsBytesSync(),
  );

  Future<void> meta(Uint8List bytes) => event('dataChannelEventpeermeta', {
    'event': 'dataChannelReceiveMessage',
    'id': 1,
    'type': 'binary',
    'data': bytes,
  });
}
