import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../mobile_services.dart';

import 'kit/kit.dart';
export 'kit/kit.dart';

class OSIcon extends StatelessWidget {
  const OSIcon(this.os, {super.key, this.dim = false, this.size = 46});
  final ComputerOS os;
  final bool dim;
  final double size;

  @override
  Widget build(BuildContext context) {
    final (icon, colors) = switch (os) {
      ComputerOS.mac => (
        Icons.apple,
        const [Color(0xff5b6372), Color(0xff292d38)],
      ),
      ComputerOS.windows => (
        Icons.window_outlined,
        const [Color(0xff428fff), Color(0xff135bd2)],
      ),
      ComputerOS.linux => (
        CupertinoIcons.chevron_left_slash_chevron_right,
        const [Color(0xff56646e), Color(0xff232b36)],
      ),
      ComputerOS.unknown => (
        CupertinoIcons.desktopcomputer,
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
      child: Icon(icon, color: Colors.white, size: size * .53),
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
