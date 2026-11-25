import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'tokens.dart';

class KPress extends StatefulWidget {
  const KPress({
    super.key,
    this.onTap,
    required this.child,
    this.button = true,
  });
  final VoidCallback? onTap;
  final Widget child;
  final bool button;

  @override
  State<KPress> createState() => _KPressState();
}

class _KPressState extends State<KPress> {
  bool _pressed = false;

  @override
  Widget build(BuildContext context) {
    final tap = widget.onTap;
    return Semantics(
      button: widget.button ? true : null,
      enabled: widget.button ? tap != null : null,
      child: FocusableActionDetector(
        enabled: tap != null,
        shortcuts: const {
          SingleActivator(LogicalKeyboardKey.enter): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.space): ActivateIntent(),
        },
        actions: {
          ActivateIntent: CallbackAction<ActivateIntent>(
            onInvoke: (_) => tap?.call(),
          ),
        },
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: tap,
          onTapDown: tap == null
              ? null
              : (_) => setState(() => _pressed = true),
          onTapUp: tap == null ? null : (_) => setState(() => _pressed = false),
          onTapCancel: tap == null
              ? null
              : () => setState(() => _pressed = false),
          child: AnimatedOpacity(
            duration: const Duration(milliseconds: 110),
            opacity: _pressed && tap != null ? .58 : 1,
            child: widget.child,
          ),
        ),
      ),
    );
  }
}

enum KButtonVariant { primary, secondary, text, danger }

class KButton extends StatelessWidget {
  const KButton({
    super.key,
    required this.label,
    this.onTap,
    this.variant = KButtonVariant.primary,
    this.compact = false,
    this.fill,
    this.ink,
  });
  final String label;
  final VoidCallback? onTap;
  final KButtonVariant variant;
  final bool compact;
  final Color? fill;
  final Color? ink;

  @override
  Widget build(BuildContext context) {
    final (background, foreground) = switch (variant) {
      KButtonVariant.primary => (const Color(0xff1669e7), Colors.white),
      KButtonVariant.secondary => (context.blueTint, context.action),
      KButtonVariant.text => (Colors.transparent, context.action),
      KButtonVariant.danger => (
        context.isDark ? const Color(0xff462626) : const Color(0xffffeae9),
        context.isDark ? const Color(0xffff8c86) : const Color(0xffbc271e),
      ),
    };
    return KPress(
      onTap: onTap,
      child: Opacity(
        opacity: onTap == null ? .45 : 1,
        child: Container(
          constraints: BoxConstraints(
            minHeight: compact ? 35 : 46,
            minWidth: compact ? 38 : 54,
          ),
          alignment: Alignment.center,
          padding: EdgeInsets.symmetric(
            horizontal: compact ? 11 : 12,
            vertical: compact ? 5 : 10,
          ),
          decoration: BoxDecoration(
            color: fill ?? background,
            borderRadius: BorderRadius.circular(compact ? 9 : 12),
          ),
          child: Text(
            label,
            textAlign: TextAlign.center,
            style: TextStyle(
              fontSize: compact ? 13 : 15,
              fontWeight: FontWeight.w600,
              color: ink ?? foreground,
            ),
          ),
        ),
      ),
    );
  }
}

class KIconButton extends StatelessWidget {
  const KIconButton({
    super.key,
    required this.icon,
    required this.tooltip,
    this.onPressed,
    this.color,
    this.extent = 44,
  });
  final Widget icon;
  final String tooltip;
  final VoidCallback? onPressed;
  final Color? color;
  final double extent;

  @override
  Widget build(BuildContext context) => Tooltip(
    message: tooltip,
    decoration: BoxDecoration(
      color: context.ink,
      borderRadius: BorderRadius.circular(8),
    ),
    textStyle: TextStyle(color: context.card, fontSize: 13),
    child: KPress(
      onTap: onPressed,
      child: SizedBox.square(
        dimension: extent,
        child: Center(
          child: IconTheme.merge(
            data: IconThemeData(color: color ?? context.action),
            child: icon,
          ),
        ),
      ),
    ),
  );
}

class KInput extends StatelessWidget {
  const KInput({
    super.key,
    required this.controller,
    this.label,
    this.hint,
    this.leading,
    this.trailing,
    this.error,
    this.autofocus = false,
    this.obscureText = false,
    this.keyboardType,
    this.textInputAction,
    this.inputFormatters,
    this.textAlign = TextAlign.start,
    this.onChanged,
    this.onSubmitted,
    this.focusNode,
    this.clearLabel = '清除',
  });
  final TextEditingController controller;
  final String? label;
  final String? hint;
  final Widget? leading;
  final Widget? trailing;
  final String? error;
  final bool autofocus;
  final bool obscureText;
  final TextInputType? keyboardType;
  final TextInputAction? textInputAction;
  final List<TextInputFormatter>? inputFormatters;
  final TextAlign textAlign;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;
  final FocusNode? focusNode;
  final String clearLabel;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    mainAxisSize: MainAxisSize.min,
    children: [
      if (label != null)
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 0, 4, 6),
          child: Text(
            label!,
            style: TextStyle(
              color: context.muted,
              fontSize: 13,
              fontWeight: FontWeight.w500,
            ),
          ),
        ),
      DecoratedBox(
        decoration: BoxDecoration(
          color: context.recessed,
          borderRadius: BorderRadius.circular(11),
        ),
        child: Row(
          children: [
            if (leading != null)
              Padding(
                padding: const EdgeInsets.only(left: 12),
                child: leading!,
              ),
            Expanded(
              child: CupertinoTextField(
                controller: controller,
                focusNode: focusNode,
                autofocus: autofocus,
                placeholder: hint,
                placeholderStyle: TextStyle(color: context.muted, fontSize: 15),
                style: TextStyle(color: context.ink, fontSize: 16),
                cursorColor: context.action,
                decoration: null,
                padding: const EdgeInsets.fromLTRB(12, 12, 6, 12),
                obscureText: obscureText,
                autocorrect: false,
                enableSuggestions: !obscureText,
                keyboardAppearance: Theme.of(context).brightness,
                keyboardType: keyboardType,
                textInputAction: textInputAction,
                inputFormatters: inputFormatters,
                textAlign: textAlign,
                onChanged: onChanged,
                onSubmitted: onSubmitted,
              ),
            ),
            ListenableBuilder(
              listenable: controller,
              builder: (context, _) => controller.text.isNotEmpty
                  ? KIconButton(
                      tooltip: clearLabel,
                      extent: 40,
                      color: context.muted,
                      icon: const Icon(
                        CupertinoIcons.clear_circled_solid,
                        size: 17,
                      ),
                      onPressed: () {
                        controller.clear();
                        onChanged?.call('');
                      },
                    )
                  : const SizedBox(width: 10),
            ),
            ?trailing,
          ],
        ),
      ),
      if (error != null)
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 5, 4, 0),
          child: Text(error!, style: const TextStyle(color: red, fontSize: 12)),
        ),
    ],
  );
}

class KRule extends StatelessWidget {
  const KRule({super.key, this.indent = 0, this.color});
  final double indent;
  final Color? color;

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(left: indent),
    child: ColoredBox(
      color: color ?? context.line,
      child: SizedBox(
        height: 1 / MediaQuery.devicePixelRatioOf(context),
        width: double.infinity,
      ),
    ),
  );
}

class KRow extends StatelessWidget {
  const KRow({
    super.key,
    required this.title,
    this.subtitle,
    this.leading,
    this.trailing,
    this.onTap,
    this.chevron = false,
    this.dimmed = false,
  });
  final String title;
  final String? subtitle;
  final Widget? leading;
  final Widget? trailing;
  final VoidCallback? onTap;
  final bool chevron;
  final bool dimmed;

  @override
  Widget build(BuildContext context) {
    final row = Padding(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 42),
        child: Row(
          children: [
            if (leading != null) ...[leading!, const SizedBox(width: 12)],
            Expanded(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: dimmed ? context.muted : context.ink,
                      fontWeight: FontWeight.w600,
                      fontSize: 16,
                    ),
                  ),
                  if (subtitle != null) ...[
                    const SizedBox(height: 3),
                    Text(
                      subtitle!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: context.muted, fontSize: 13),
                    ),
                  ],
                ],
              ),
            ),
            if (trailing != null) ...[const SizedBox(width: 8), trailing!],
            if (chevron) ...[
              const SizedBox(width: 7),
              Icon(
                CupertinoIcons.chevron_right,
                color: context.muted,
                size: 14,
              ),
            ],
          ],
        ),
      ),
    );
    return onTap == null ? row : KPress(onTap: onTap, child: row);
  }
}

class KPageHeader extends StatelessWidget {
  const KPageHeader(this.title, {super.key});
  final String title;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    mainAxisSize: MainAxisSize.min,
    children: [
      KIconButton(
        tooltip: '返回',
        icon: const Icon(CupertinoIcons.chevron_left),
        onPressed: () => Navigator.maybePop(context),
      ),
      Padding(
        padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
        child: PageTitle(title),
      ),
    ],
  );
}

class KLoader extends StatelessWidget {
  const KLoader({super.key, this.color, this.radius = 8});
  final Color? color;
  final double radius;

  @override
  Widget build(BuildContext context) => CupertinoActivityIndicator(
    radius: radius,
    color: color ?? context.action,
  );
}
