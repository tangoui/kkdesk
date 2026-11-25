import 'package:flutter/material.dart';

import 'controls.dart';

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
      ClipRRect(
        borderRadius: BorderRadius.circular(15),
        child: ColoredBox(
          color: context.card,
          child: Column(
            children: [
              for (var index = 0; index < children.length; index++) ...[
                if (index > 0) KRule(indent: 64),
                children[index],
              ],
            ],
          ),
        ),
      ),
    ],
  );
}
