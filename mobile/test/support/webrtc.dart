import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

class TestWebRTC {
  TestWebRTC()
    : messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger {
    const method = MethodChannel('FlutterWebRTC.Method');
    messenger.setMockMethodCallHandler(method, (call) async {
      calls.add(call.method);
      if (call.method == 'dataChannelSend' &&
          call.arguments['dataChannelId'] == 'meta') {
        sentMeta.add(List<int>.of(call.arguments['data'] as Uint8List));
        onMetaSent?.call(sentMeta.last);
      }
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
  final sentMeta = <List<int>>[];
  void Function(List<int>)? onMetaSent;
  final listeners = <String>[];

  Future<void> event(String stream, Map<String, Object> data) async {
    await messenger.handlePlatformMessage(
      'FlutterWebRTC/$stream',
      const StandardMethodCodec().encodeSuccessEnvelope(data),
      null,
    );
  }

  Future<void> openChannels() async {
    for (final label in ['input-move', 'input', 'meta', 'files']) {
      await event('dataChannelEventpeer$label', {
        'event': 'dataChannelStateChanged',
        'id': 1,
        'state': 'open',
      });
    }
  }

  Future<void> connected() => event('peerConnectionEventpeer', {
    'event': 'peerConnectionState',
    'state': 'connected',
  });

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
