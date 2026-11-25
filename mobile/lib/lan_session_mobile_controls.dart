part of 'lan_session.dart';

/// Additional UI input over the established channels, using the desktop
/// input protocol. Drag moves share the reliable channel with button events.
extension LanSessionMobileControls on LanSession {
  void sendDragMove(double x, double y) =>
      unawaited(_sendBinary(_input, mouseMove(x, y)));

  void sendButton(double x, double y, int button, bool down, int modifiers) {
    final bytes = mouseButton(x, y, down)
      ..[9] = button
      ..[11] = modifiers;
    unawaited(_sendBinary(_input, bytes));
  }

  void sendKey(int hid, bool down, int modifiers) {
    final data = ByteData(5)
      ..setUint8(0, 3)
      ..setUint16(1, hid, Endian.little)
      ..setUint8(3, down ? 1 : 0)
      ..setUint8(4, modifiers);
    unawaited(_sendBinary(_input, data.buffer.asUint8List()));
  }

  void sendWheel(double dx, double dy, int modifiers) {
    final data = ByteData(10)
      ..setUint8(0, 4)
      ..setFloat32(1, dx, Endian.little)
      ..setFloat32(5, dy, Endian.little)
      ..setUint8(9, modifiers);
    unawaited(_sendBinary(_input, data.buffer.asUint8List()));
  }

  Future<({int? latency, int? fps})> mobileStats() async {
    final latency = _clock.best?.rtt;
    final reports = await _peer?.getStats();
    final video = reports
        ?.where(
          (report) =>
              report.type == 'inbound-rtp' && report.values['kind'] == 'video',
        )
        .firstOrNull;
    final fps = video?.values['framesPerSecond'];
    return (
      latency: latency == null ? null : (latency / 1000000).round(),
      fps: fps is num ? fps.round() : null,
    );
  }
}
