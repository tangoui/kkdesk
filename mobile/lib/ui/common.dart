import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../mobile_services.dart';

const blue = Color(0xff1677ff);
const green = Color(0xff34c759);
const amber = Color(0xffff9f0a);
const red = Color(0xffff453a);

extension PageColors on BuildContext {
  bool get isDark => Theme.of(this).brightness == Brightness.dark;
  Color get ink => isDark ? const Color(0xfff5f5f7) : const Color(0xff151518);
  Color get muted => isDark ? const Color(0xffa0a0aa) : const Color(0xff696974);
  Color get background => isDark ? Colors.black : const Color(0xfff2f2f7);
  Color get card => isDark ? const Color(0xff1c1c1e) : Colors.white;
  Color get recessed =>
      isDark ? const Color(0xff2c2c2e) : const Color(0xffe5e5ea);
  Color get line => isDark ? const Color(0xff38383a) : const Color(0xffe4e4e9);
  Color get action => isDark ? const Color(0xff6aaaff) : blue;
  Color get blueTint =>
      isDark ? const Color(0xff203450) : const Color(0xffe8f1ff);
}

class PageTitle extends StatelessWidget {
  const PageTitle(this.title, {super.key});

  final String title;

  @override
  Widget build(BuildContext context) => Text(
    title,
    style: TextStyle(
      color: context.ink,
      fontSize: 36,
      height: 1.25,
      fontWeight: FontWeight.w700,
      letterSpacing: -.9,
    ),
  );
}

class GroupCard extends StatelessWidget {
  const GroupCard({super.key, this.title, required this.children});
  final String? title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      if (title case final title?)
        Padding(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 7),
          child: Text(
            title,
            style: TextStyle(color: context.muted, fontSize: 13),
          ),
        ),
      Material(
        color: context.card,
        clipBehavior: Clip.antiAlias,
        borderRadius: BorderRadius.circular(15),
        child: Column(
          children: [
            for (var index = 0; index < children.length; index++) ...[
              if (index > 0)
                Divider(
                  height: 1,
                  thickness: 1,
                  indent: 70,
                  color: context.line,
                ),
              children[index],
            ],
          ],
        ),
      ),
    ],
  );
}

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

class SheetHandle extends StatelessWidget {
  const SheetHandle({super.key});

  @override
  Widget build(BuildContext context) => Center(
    child: Container(
      width: 36,
      height: 4,
      decoration: BoxDecoration(
        color: context.recessed,
        borderRadius: BorderRadius.circular(20),
      ),
    ),
  );
}

class SheetBody extends StatelessWidget {
  const SheetBody({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) => SafeArea(
    top: false,
    child: Padding(
      padding: EdgeInsets.fromLTRB(
        22,
        11,
        22,
        14 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [const SheetHandle(), const SizedBox(height: 18), child],
        ),
      ),
    ),
  );
}

Future<T?> appSheet<T>(BuildContext context, WidgetBuilder builder) =>
    showModalBottomSheet<T>(
      context: context,
      backgroundColor: context.card,
      useSafeArea: true,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(27)),
      ),
      builder: (context) => SheetBody(child: builder(context)),
    );

Future<String?> editText(
  BuildContext context, {
  required String title,
  required String label,
  required String initial,
  String? hint,
  bool required = true,
}) async {
  return showDialog<String>(
    context: context,
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
  Widget build(BuildContext context) => AlertDialog(
    title: Text(widget.title),
    content: TextField(
      autofocus: true,
      controller: _controller,
      decoration: InputDecoration(
        labelText: widget.label,
        hintText: widget.hint,
      ),
      textInputAction: TextInputAction.done,
      onChanged: (_) => setState(() {}),
      onSubmitted: (_) => _save(),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      TextButton(onPressed: _valid ? _save : null, child: const Text('保存')),
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
