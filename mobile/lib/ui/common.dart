import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../lan_auth.dart';
import '../mobile_services.dart';

import 'kit/kit.dart';
export 'kit/kit.dart';

class BrandMark extends StatelessWidget {
  const BrandMark({super.key, required this.size});

  final double size;

  @override
  Widget build(BuildContext context) => Image.asset(
    'assets/kkdesk_mark.png',
    width: size,
    height: size,
    excludeFromSemantics: true,
  );
}

class OSIcon extends StatelessWidget {
  const OSIcon(this.os, {super.key, this.dim = false, this.size = 46});
  final ComputerOS os;
  final bool dim;
  final double size;

  @override
  Widget build(BuildContext context) {
    final (asset, colors) = switch (os) {
      ComputerOS.mac => (
        'apple.svg',
        const [Color(0xff5b6372), Color(0xff292d38)],
      ),
      ComputerOS.windows => (
        'windows.svg',
        const [Color(0xff428fff), Color(0xff135bd2)],
      ),
      ComputerOS.linux => (
        'linux.svg',
        const [Color(0xff56646e), Color(0xff232b36)],
      ),
      ComputerOS.unknown => (
        null,
        const [Color(0xff6b778a), Color(0xff465364)],
      ),
    };
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(size * .27),
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: dim ? const [Color(0xff9d9da2), Color(0xff77777e)] : colors,
        ),
      ),
      child: Center(
        child: asset == null
            ? Icon(
                CupertinoIcons.desktopcomputer,
                color: Colors.white,
                size: size * .53,
              )
            : SvgPicture.asset(
                'assets/os/$asset',
                width: size * .53,
                height: size * .53,
                colorFilter: const ColorFilter.mode(
                  Colors.white,
                  BlendMode.srcIn,
                ),
              ),
      ),
    );
  }
}

Future<String?> editText(
  BuildContext context, {
  required String title,
  required String label,
  required String initial,
  String? hint,
  bool required = true,
}) async {
  return kDialog<String>(
    context,
    builder: (context) => _TextDialog(
      title: title,
      label: label,
      initial: initial,
      hint: hint,
      required: required,
    ),
  );
}

class _TextDialog extends StatefulWidget {
  const _TextDialog({
    required this.title,
    required this.label,
    required this.initial,
    required this.hint,
    required this.required,
  });

  final String title;
  final String label;
  final String initial;
  final String? hint;
  final bool required;

  @override
  State<_TextDialog> createState() => _TextDialogState();
}

class _TextDialogState extends State<_TextDialog> {
  late final _controller = TextEditingController(text: widget.initial);
  bool get _valid => !widget.required || _controller.text.trim().isNotEmpty;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _save() {
    if (_valid) Navigator.pop(context, _controller.text.trim());
  }

  @override
  Widget build(BuildContext context) => KDialog(
    title: widget.title,
    content: KInput(
      label: widget.label,
      hint: widget.hint,
      controller: _controller,
      textInputAction: TextInputAction.done,
      onChanged: (_) => setState(() {}),
      onSubmitted: (_) => _save(),
    ),
    actions: [
      KButton(
        label: '取消',
        variant: KButtonVariant.secondary,
        onTap: () => Navigator.pop(context),
      ),
      KButton(label: '保存', onTap: _valid ? _save : null),
    ],
  );
}

Future<void> renameComputer(
  BuildContext context,
  DeviceRepository devices,
  Computer computer,
) async {
  final name = await editText(
    context,
    title: '更改名称',
    label: '电脑名称',
    initial: computer.name,
  );
  if (name != null) await devices.rename(computer.id, name);
}

Future<bool> confirmForgetLanIdentity(
  BuildContext context,
  LanIdentityChanged identity,
) async {
  final confirmed = await kConfirm(
    context,
    title: '忘记这台电脑的身份',
    message: '只有确认对方重装了 kkdesk 时才这样做，否则可能连到冒充的电脑。',
    confirm: '忘记身份',
    danger: true,
  );
  if (confirmed != true || !context.mounted) return false;
  try {
    await identity.forget();
    return true;
  } catch (_) {
    if (context.mounted) {
      KNotice.show(context, '无法忘记这台电脑的身份', kind: KNoticeKind.error);
    }
    return false;
  }
}
