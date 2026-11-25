import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../mobile_services.dart';
import 'common.dart';

class ScanPage extends StatefulWidget {
  const ScanPage({super.key, required this.camera, this.preview = false});
  final bool camera;
  final bool preview;

  @override
  State<ScanPage> createState() => _ScanPageState();
}

class _ScanPageState extends State<ScanPage> {
  MobileScannerController? _scanner;
  bool _busy = false;
  bool _finishing = false;
  bool _previewTorch = false;
  final _picker = ImagePicker();

  @override
  void initState() {
    super.initState();
    if (widget.camera) {
      _scanner = MobileScannerController(
        formats: const [BarcodeFormat.qrCode],
        detectionSpeed: DetectionSpeed.noDuplicates,
      );
    }
  }

  @override
  void dispose() {
    unawaited(_scanner?.dispose());
    super.dispose();
  }

  String? _extract(BarcodeCapture? capture) {
    for (final barcode in capture?.barcodes ?? const <Barcode>[]) {
      final value = barcode.rawValue;
      if (value != null && value.startsWith('kkdesk://pair?')) return value;
    }
    return null;
  }

  void _finish(String result) {
    if (!mounted || _finishing) return;
    _finishing = true;
    Navigator.pop(context, result);
  }

  Future<void> _code() async {
    if (_busy) return;
    setState(() => _busy = true);
    final code = await appSheet<String>(context, (_) => const _ManualCode());
    if (!mounted) return;
    if (code != null) {
      _finish(code);
    } else {
      setState(() => _busy = false);
    }
  }

  Future<void> _album() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final file = await _picker.pickImage(source: ImageSource.gallery);
      if (file == null || !mounted) return;
      final temporary = _scanner == null
          ? MobileScannerController(
              autoStart: false,
              formats: const [BarcodeFormat.qrCode],
            )
          : null;
      try {
        final result = _extract(
          await (_scanner ?? temporary!).analyzeImage(file.path),
        );
        if (!mounted) return;
        if (result != null) {
          _finish(result);
        } else {
          KNotice.show(
            context,
            '相册图片中没有 kkdesk 配对二维码',
            kind: KNoticeKind.error,
          );
        }
      } finally {
        await temporary?.dispose();
      }
    } on Exception {
      if (mounted) {
        KNotice.show(context, '无法读取这张照片', kind: KNoticeKind.error);
      }
    } finally {
      if (mounted && !_finishing) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AnnotatedRegion<SystemUiOverlayStyle>(
    value: SystemUiOverlayStyle.light,
    child: Scaffold(
      backgroundColor: const Color(0xff141518),
      body: LayoutBuilder(
        builder: (context, bounds) {
          final edge = math
              .min(bounds.maxWidth * .67, bounds.maxHeight * .38)
              .clamp(80.0, 360.0);
          final scanBox = Rect.fromCenter(
            center: Offset(bounds.maxWidth / 2, bounds.maxHeight * .425),
            width: edge,
            height: edge,
          );
          return Stack(
            fit: StackFit.expand,
            children: [
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: RadialGradient(
                    radius: .85,
                    center: Alignment(0, -.18),
                    colors: [
                      Color(0xff666a73),
                      Color(0xff303238),
                      Color(0xff141518),
                    ],
                  ),
                ),
              ),
              if (_scanner case final scanner?)
                MobileScanner(
                  controller: scanner,
                  scanWindow: scanBox,
                  onDetect: (capture) {
                    final result = _extract(capture);
                    if (!_busy && result != null) _finish(result);
                  },
                  errorBuilder: (context, error) => Align(
                    alignment: const Alignment(0, -.20),
                    child: Icon(
                      error.errorCode == MobileScannerErrorCode.permissionDenied
                          ? CupertinoIcons.camera_fill
                          : CupertinoIcons.camera,
                      size: 60,
                      color: Colors.white38,
                    ),
                  ),
                ),
              if (widget.preview)
                Positioned.fromRect(
                  rect: scanBox.deflate(18),
                  child: Transform.rotate(
                    angle: -.055,
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        color: const Color(0xffe9eaee),
                        borderRadius: BorderRadius.circular(10),
                        boxShadow: const [
                          BoxShadow(
                            color: Colors.black54,
                            blurRadius: 35,
                            offset: Offset(0, 15),
                          ),
                        ],
                      ),
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: ColoredBox(
                          color: Colors.white,
                          child: Center(
                            child: Icon(
                              Icons.qr_code_2,
                              color: Colors.black,
                              size: edge * .52,
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              IgnorePointer(child: CustomPaint(painter: _ScanMask(scanBox))),
              SafeArea(
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(22, 10, 22, 20),
                  child: Column(
                    children: [
                      Row(
                        mainAxisAlignment: MainAxisAlignment.spaceBetween,
                        children: [
                          _ScanAction(
                            label: '关闭扫码',
                            icon: CupertinoIcons.xmark,
                            onTap: () => Navigator.pop(context),
                          ),
                          const Text(
                            '扫码配对',
                            style: TextStyle(
                              color: Colors.white,
                              fontSize: 19,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                          if (_scanner case final scanner?)
                            ValueListenableBuilder<MobileScannerState>(
                              valueListenable: scanner,
                              builder: (_, state, _) => _ScanAction(
                                label: '手电筒',
                                icon: state.torchState == TorchState.on
                                    ? CupertinoIcons.bolt_fill
                                    : CupertinoIcons.bolt,
                                onTap:
                                    state.isRunning &&
                                        state.torchState !=
                                            TorchState.unavailable
                                    ? () => unawaited(scanner.toggleTorch())
                                    : null,
                              ),
                            )
                          else
                            _ScanAction(
                              label: '手电筒',
                              icon: _previewTorch
                                  ? CupertinoIcons.bolt_fill
                                  : CupertinoIcons.bolt,
                              onTap: widget.preview
                                  ? () => setState(
                                      () => _previewTorch = !_previewTorch,
                                    )
                                  : null,
                            ),
                        ],
                      ),
                      const Spacer(),
                      Row(
                        children: [
                          Expanded(
                            child: _ScanAction(
                              label: '输入配对码',
                              icon: CupertinoIcons.keyboard,
                              onTap: _busy ? null : _code,
                              wide: true,
                            ),
                          ),
                          const SizedBox(width: 12),
                          _ScanAction(
                            label: '从相册识别',
                            icon: CupertinoIcons.photo,
                            onTap: _busy ? null : _album,
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
              Positioned(
                top: scanBox.bottom + 20,
                left: 18,
                right: 18,
                child: Column(
                  children: [
                    const Text(
                      '对准电脑上 kkdesk 的二维码',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        color: Colors.white,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 6),
                    Text(
                      widget.camera || widget.preview
                          ? '电脑端：连接 → 手机扫码连接'
                          : '可以输入配对码或从相册识别',
                      textAlign: TextAlign.center,
                      style: const TextStyle(
                        color: Colors.white60,
                        fontSize: 13,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          );
        },
      ),
    ),
  );
}

class _ScanAction extends StatelessWidget {
  const _ScanAction({
    required this.label,
    required this.icon,
    required this.onTap,
    this.wide = false,
  });
  final String label;
  final IconData icon;
  final VoidCallback? onTap;
  final bool wide;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      color: Colors.white.withValues(alpha: .16),
      borderRadius: BorderRadius.circular(wide ? 14 : 25),
    ),
    child: KPress(
      onTap: onTap,
      child: Tooltip(
        message: wide ? '' : label,
        child: SizedBox(
          height: wide ? 53 : 45,
          width: wide ? null : 48,
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(
                icon,
                color: onTap == null ? Colors.white38 : Colors.white,
                size: 21,
              ),
              if (wide) ...[
                const SizedBox(width: 8),
                Text(
                  label,
                  style: const TextStyle(
                    color: Colors.white,
                    fontSize: 15,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    ),
  );
}

class _ScanMask extends CustomPainter {
  const _ScanMask(this.box);
  final Rect box;

  @override
  void paint(Canvas canvas, Size size) {
    final hole = RRect.fromRectAndRadius(box, const Radius.circular(22));
    canvas.drawPath(
      Path()
        ..fillType = PathFillType.evenOdd
        ..addRect(Offset.zero & size)
        ..addRRect(hole),
      Paint()..color = Colors.black45,
    );
    final corner = Path()
      ..moveTo(0, 36)
      ..lineTo(0, 16)
      ..quadraticBezierTo(0, 0, 16, 0)
      ..lineTo(36, 0);
    final paint = Paint()
      ..color = Colors.white
      ..style = PaintingStyle.stroke
      ..strokeWidth = 3
      ..strokeCap = StrokeCap.round;
    for (var index = 0; index < 4; index++) {
      canvas.save();
      canvas.translate(box.center.dx, box.center.dy);
      canvas.rotate(index * math.pi / 2);
      canvas.translate(-box.width / 2, -box.height / 2);
      canvas.drawPath(corner, paint);
      canvas.restore();
    }
    final line = Rect.fromCenter(
      center: box.center,
      width: box.width - 27,
      height: 2,
    );
    final gradient = const LinearGradient(
      colors: [Colors.transparent, Color(0xff4096ff), Colors.transparent],
    );
    canvas.drawRect(
      line.inflate(2),
      Paint()
        ..shader = gradient.createShader(line)
        ..maskFilter = const MaskFilter.blur(BlurStyle.normal, 5),
    );
    canvas.drawRect(line, Paint()..shader = gradient.createShader(line));
  }

  @override
  bool shouldRepaint(_ScanMask oldDelegate) => oldDelegate.box != box;
}

class _ManualCode extends StatefulWidget {
  const _ManualCode();

  @override
  State<_ManualCode> createState() => _ManualCodeState();
}

class _ManualCodeState extends State<_ManualCode> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() {
    if (_controller.text.length == 8) Navigator.pop(context, _controller.text);
  }

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text(
        '输入配对码',
        style: Theme.of(context).textTheme.titleLarge,
        textAlign: TextAlign.center,
      ),
      const SizedBox(height: 8),
      Text(
        '输入电脑上显示的 8 位数字',
        style: TextStyle(color: context.muted),
        textAlign: TextAlign.center,
      ),
      const SizedBox(height: 18),
      KInput(
        key: const ValueKey('pair-code'),
        autofocus: true,
        controller: _controller,
        keyboardType: TextInputType.number,
        inputFormatters: [
          FilteringTextInputFormatter.digitsOnly,
          LengthLimitingTextInputFormatter(8),
        ],
        onChanged: (_) => setState(() {}),
        onSubmitted: (_) => _submit(),
        textInputAction: TextInputAction.done,
        textAlign: TextAlign.center,
        hint: '00000000',
      ),
      const SizedBox(height: 14),
      KButton(
        label: '继续',
        onTap: _controller.text.length == 8 ? _submit : null,
      ),
    ],
  );
}

class PairVerification extends StatelessWidget {
  const PairVerification({super.key, required this.request});
  final PairRequest request;

  @override
  Widget build(BuildContext context) => StreamBuilder<PairStatus>(
    stream: request.statuses,
    initialData: PairStatus.waiting,
    builder: (context, snapshot) {
      final status = snapshot.hasError ? PairStatus.timedOut : snapshot.data!;
      final (title, message, icon, color) = switch (status) {
        PairStatus.waiting => (
          '配对这台电脑？',
          '电脑上显示同样的 4 位数字\n再在电脑上点「允许配对」',
          null,
          context.action,
        ),
        PairStatus.success => (
          '配对成功',
          '这台电脑已加入设备列表。',
          CupertinoIcons.check_mark_circled_solid,
          green,
        ),
        PairStatus.rejected => (
          '配对被拒绝',
          '电脑拒绝了配对请求。',
          CupertinoIcons.xmark_circle_fill,
          red,
        ),
        PairStatus.timedOut => (
          '配对超时',
          '电脑未在规定时间内确认。',
          CupertinoIcons.clock_fill,
          amber,
        ),
        PairStatus.expired => (
          '二维码已过期',
          '请在电脑上刷新二维码，然后重新扫描。',
          CupertinoIcons.exclamationmark_circle_fill,
          amber,
        ),
      };
      final waiting = status == PairStatus.waiting;
      final success = status == PairStatus.success;
      return Column(
        children: [
          if (waiting)
            OSIcon(request.computer.os, size: 68)
          else
            Icon(icon, color: color, size: 68),
          const SizedBox(height: 12),
          Text(
            title,
            style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 22),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 6),
          Text(
            request.computer.name,
            textAlign: TextAlign.center,
            style: const TextStyle(fontSize: 16),
          ),
          Text(
            '${request.computer.os.label} · ${request.server == null ? '局域网直连' : '经 ${request.server}'}',
            textAlign: TextAlign.center,
            style: TextStyle(color: context.muted, fontSize: 13),
          ),
          if (waiting) ...[
            const SizedBox(height: 20),
            Text('核对码', style: TextStyle(color: context.muted, fontSize: 13)),
            const SizedBox(height: 7),
            Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                for (final digit in request.verificationCode.split(''))
                  Container(
                    margin: const EdgeInsets.symmetric(horizontal: 4),
                    width: 47,
                    height: 56,
                    alignment: Alignment.center,
                    decoration: BoxDecoration(
                      color: context.background,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Text(
                      digit,
                      style: TextStyle(
                        color: context.action,
                        fontSize: 29,
                        fontWeight: FontWeight.w700,
                        fontFeatures: const [FontFeature.tabularFigures()],
                      ),
                    ),
                  ),
              ],
            ),
          ],
          const SizedBox(height: 12),
          Text(
            message,
            textAlign: TextAlign.center,
            style: TextStyle(color: context.muted, height: 1.55),
          ),
          const SizedBox(height: 20),
          if (waiting)
            Container(
              height: 49,
              width: double.infinity,
              decoration: BoxDecoration(
                color: context.blueTint,
                borderRadius: BorderRadius.circular(13),
              ),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const KLoader(),
                  const SizedBox(width: 9),
                  Text(
                    '等待电脑确认…',
                    style: TextStyle(
                      color: context.action,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            )
          else
            SizedBox(
              width: double.infinity,
              child: KButton(
                onTap: () => Navigator.pop(context, status),
                label: success ? '完成' : '重新扫码',
              ),
            ),
          if (!success)
            KButton(
              onTap: () => Navigator.pop(context),
              label: '取消',
              variant: KButtonVariant.text,
            ),
        ],
      );
    },
  );
}
