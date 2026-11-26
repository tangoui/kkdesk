import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'controls.dart';
import 'tokens.dart';

Widget _overlayText(BuildContext context, Widget child) => DefaultTextStyle(
  style: Theme.of(context).textTheme.bodyMedium!.copyWith(color: context.ink),
  child: child,
);

Future<T?> kDialog<T>(BuildContext context, {required WidgetBuilder builder}) =>
    showGeneralDialog<T>(
      context: context,
      barrierDismissible: true,
      barrierLabel: '关闭对话框',
      barrierColor: Colors.black54,
      transitionDuration: const Duration(milliseconds: 210),
      pageBuilder: (context, _, _) => SafeArea(
        child: Center(child: _overlayText(context, builder(context))),
      ),
      transitionBuilder: (_, animation, _, child) {
        final curve = animation.drive(CurveTween(curve: Curves.easeOutCubic));
        return FadeTransition(
          opacity: curve,
          child: ScaleTransition(
            scale: curve.drive(Tween(begin: .95, end: 1.0)),
            child: child,
          ),
        );
      },
    );

class KDialog extends StatelessWidget {
  const KDialog({
    super.key,
    required this.title,
    this.content,
    this.leading,
    required this.actions,
    this.stacked = false,
  });
  final String title;
  final Widget? content;
  final Widget? leading;
  final List<Widget> actions;
  final bool stacked;

  @override
  Widget build(BuildContext context) => AnimatedPadding(
    duration: const Duration(milliseconds: 170),
    padding: EdgeInsets.fromLTRB(
      22,
      14,
      22,
      14 + MediaQuery.viewInsetsOf(context).bottom,
    ),
    child: Align(
      alignment: Alignment.center,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 360),
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: context.card,
            borderRadius: BorderRadius.circular(14),
          ),
          child: SingleChildScrollView(
            padding: const EdgeInsets.fromLTRB(19, 19, 19, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (leading != null) ...[
                  Center(child: leading),
                  const SizedBox(height: 10),
                ],
                Text(
                  title,
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: context.ink,
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (content != null) ...[
                  const SizedBox(height: 13),
                  DefaultTextStyle.merge(
                    style: TextStyle(
                      color: context.muted,
                      height: 1.5,
                      fontSize: 14,
                    ),
                    textAlign: TextAlign.center,
                    child: content!,
                  ),
                ],
                const SizedBox(height: 17),
                if (stacked || MediaQuery.textScalerOf(context).scale(14) > 19)
                  Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    spacing: 8,
                    children: actions,
                  )
                else
                  Row(
                    spacing: 9,
                    children: [
                      for (final action in actions) Expanded(child: action),
                    ],
                  ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

Future<bool?> kConfirm(
  BuildContext context, {
  required String title,
  String? message,
  required String confirm,
  bool danger = false,
}) => kDialog<bool>(
  context,
  builder: (context) => KDialog(
    title: title,
    content: message == null ? null : Text(message),
    actions: [
      KButton(
        label: '取消',
        variant: KButtonVariant.secondary,
        onTap: () => Navigator.pop(context),
      ),
      KButton(
        label: confirm,
        variant: danger ? KButtonVariant.danger : KButtonVariant.primary,
        onTap: () => Navigator.pop(context, true),
      ),
    ],
  ),
);

Future<T?> appSheet<T>(
  BuildContext context,
  WidgetBuilder builder, {
  bool grouped = false,
}) => showGeneralDialog<T>(
  context: context,
  barrierDismissible: true,
  barrierLabel: '关闭操作表',
  barrierColor: Colors.black54,
  transitionDuration: const Duration(milliseconds: 250),
  pageBuilder: (context, _, _) => SafeArea(
    bottom: false,
    child: Align(
      alignment: Alignment.bottomCenter,
      child: AnimatedPadding(
        duration: const Duration(milliseconds: 180),
        padding: EdgeInsets.fromLTRB(
          grouped ? 12 : 0,
          6,
          grouped ? 12 : 0,
          MediaQuery.viewInsetsOf(context).bottom,
        ),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 570),
          child: grouped
              ? SafeArea(
                  top: false,
                  minimum: const EdgeInsets.only(bottom: 10),
                  child: SingleChildScrollView(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        ClipRRect(
                          borderRadius: BorderRadius.circular(16),
                          child: ColoredBox(
                            color: context.card,
                            child: _overlayText(context, builder(context)),
                          ),
                        ),
                        const SizedBox(height: 8),
                        DecoratedBox(
                          decoration: BoxDecoration(
                            color: context.card,
                            borderRadius: BorderRadius.circular(16),
                          ),
                          child: _overlayText(
                            context,
                            KButton(
                              label: '取消',
                              variant: KButtonVariant.text,
                              onTap: () => Navigator.pop(context),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                )
              : ClipRRect(
                  borderRadius: const BorderRadius.vertical(
                    top: Radius.circular(24),
                  ),
                  child: ColoredBox(
                    color: context.card,
                    child: SafeArea(
                      top: false,
                      child: SingleChildScrollView(
                        padding: const EdgeInsets.fromLTRB(21, 10, 21, 12),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Container(
                              width: 34,
                              height: 4,
                              decoration: BoxDecoration(
                                color: context.recessed,
                                borderRadius: BorderRadius.circular(4),
                              ),
                            ),
                            const SizedBox(height: 16),
                            _overlayText(context, builder(context)),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
        ),
      ),
    ),
  ),
  transitionBuilder: (_, animation, _, child) => SlideTransition(
    position: animation
        .drive(CurveTween(curve: Curves.easeOutCubic))
        .drive(Tween(begin: const Offset(0, 1), end: Offset.zero)),
    child: child,
  ),
);

class KOptionGroup extends StatelessWidget {
  const KOptionGroup({super.key, required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    mainAxisSize: MainAxisSize.min,
    children: [
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 15, horizontal: 15),
        child: Text(
          title,
          textAlign: TextAlign.center,
          style: TextStyle(
            color: context.muted,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
      for (final child in children) ...[const KRule(), child],
    ],
  );
}

enum KNoticeKind { success, error, info }

class KNotice {
  static final _active = <OverlayState, VoidCallback>{};

  static void show(
    BuildContext context,
    String title, {
    String? detail,
    KNoticeKind kind = KNoticeKind.info,
  }) {
    final overlay = Overlay.of(context);
    _active[overlay]?.call();
    final (color, icon) = switch (kind) {
      KNoticeKind.success => (green, CupertinoIcons.check_mark),
      KNoticeKind.error => (red, CupertinoIcons.exclamationmark),
      KNoticeKind.info => (blue, CupertinoIcons.info),
    };
    late final OverlayEntry entry;
    Timer? timer;
    void dismiss() {
      timer?.cancel();
      if (_active.remove(overlay) == null) return;
      entry.remove();
      entry.dispose();
    }

    entry = OverlayEntry(
      builder: (context) => Positioned(
        top: 0,
        left: 16,
        right: 16,
        child: SafeArea(
          bottom: false,
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 460),
              child: TweenAnimationBuilder<double>(
                tween: Tween(begin: 0, end: 1),
                duration: const Duration(milliseconds: 210),
                builder: (_, progress, child) => Opacity(
                  opacity: progress,
                  child: Transform.translate(
                    offset: Offset(0, -12 * (1 - progress)),
                    child: child,
                  ),
                ),
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: context.card,
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(color: context.line),
                    boxShadow: const [
                      BoxShadow(
                        color: Colors.black26,
                        blurRadius: 18,
                        offset: Offset(0, 5),
                      ),
                    ],
                  ),
                  child: Semantics(
                    liveRegion: true,
                    child: _overlayText(
                      context,
                      KRow(
                        title: title,
                        subtitle: detail,
                        leading: Container(
                          width: 33,
                          height: 33,
                          decoration: BoxDecoration(
                            color: color,
                            shape: BoxShape.circle,
                          ),
                          child: Icon(icon, color: Colors.white, size: 19),
                        ),
                        trailing: KIconButton(
                          tooltip: '关闭提示',
                          extent: 37,
                          color: context.muted,
                          icon: const Icon(CupertinoIcons.xmark, size: 15),
                          onPressed: dismiss,
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
    _active[overlay] = dismiss;
    overlay.insert(entry);
    timer = Timer(const Duration(seconds: 4), dismiss);
  }
}
