import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../mobile_services.dart';

class RemoteKeys {
  RemoteKeys(this.session);
  final RemoteSession session;
  int modifiers = 0;

  static const _hidByModifier = {1: 0xe1, 2: 0xe0, 4: 0xe2, 8: 0xe3};
  static const _plain = '1234567890\n\x1b\b\t -=[]\\;\'`,./';
  static const _shifted = '!@#\$%^&*()     _+{}|:"~<>?';
  static const _usage = [
    0x1e,
    0x1f,
    0x20,
    0x21,
    0x22,
    0x23,
    0x24,
    0x25,
    0x26,
    0x27,
    0x28,
    0x29,
    0x2a,
    0x2b,
    0x2c,
    0x2d,
    0x2e,
    0x2f,
    0x30,
    0x31,
    0x33,
    0x34,
    0x35,
    0x36,
    0x37,
    0x38,
  ];

  void press(int hid, {int extraModifiers = 0}) {
    final combined = modifiers | extraModifiers;
    session.key(hid, true, combined);
    session.key(hid, false, combined);
  }

  void toggle(int modifier) {
    final next = modifiers ^ modifier;
    modifiers = next;
    session.key(_hidByModifier[modifier]!, next & modifier != 0, next);
  }

  void release() {
    for (final modifier in _hidByModifier.keys) {
      if (modifiers & modifier != 0) toggle(modifier);
    }
  }

  void chord(List<int> bits, int hid) {
    final added = bits.where((bit) => modifiers & bit == 0).toList();
    for (final bit in added) {
      toggle(bit);
    }
    press(hid);
    for (final bit in added.reversed) {
      toggle(bit);
    }
  }

  bool type(String character) {
    final code = character.codeUnitAt(0);
    if (code >= 97 && code <= 122) {
      press(code - 97 + 4);
      return true;
    }
    if (code >= 65 && code <= 90) {
      press(code - 65 + 4, extraModifiers: 1);
      return true;
    }
    final plain = _plain.indexOf(character);
    if (plain >= 0) {
      press(_usage[plain]);
      return true;
    }
    final shifted = _shifted.indexOf(character);
    if (shifted < 0) return false;
    press(_usage[shifted], extraModifiers: 1);
    return true;
  }
}

class RemoteKeyboardBar extends StatelessWidget {
  const RemoteKeyboardBar({
    super.key,
    required this.keys,
    required this.os,
    required this.changed,
    required this.close,
  });

  final RemoteKeys keys;
  final ComputerOS os;
  final VoidCallback changed;
  final VoidCallback close;

  @override
  Widget build(BuildContext context) {
    final shortcuts = [
      ('Esc', 0x29),
      ('Tab', 0x2b),
      ('Ctrl', -2),
      ('Alt', -4),
      (os == ComputerOS.mac ? '⌘' : 'Win', -8),
      ('⇧', -1),
      ('←', 0x50),
      ('↑', 0x52),
      ('↓', 0x51),
      ('→', 0x4f),
      for (var index = 0; index < 12; index++) ('F${index + 1}', 0x3a + index),
    ];
    return Material(
      color: const Color(0xff25252c),
      child: SizedBox(
        height: 47,
        child: Row(
          children: [
            Expanded(
              child: ListView.separated(
                padding: const EdgeInsets.fromLTRB(7, 5, 7, 5),
                scrollDirection: Axis.horizontal,
                itemCount: shortcuts.length,
                separatorBuilder: (_, _) => const SizedBox(width: 6),
                itemBuilder: (context, index) {
                  final (label, hid) = shortcuts[index];
                  final latched = hid < 0 && keys.modifiers & -hid != 0;
                  return Semantics(
                    selected: hid < 0 ? latched : null,
                    child: TextButton(
                      style: TextButton.styleFrom(
                        fixedSize: const Size.fromHeight(37),
                        minimumSize: const Size(39, 36),
                        padding: const EdgeInsets.symmetric(horizontal: 8),
                        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(8),
                        ),
                        foregroundColor: Colors.white,
                        backgroundColor: latched
                            ? const Color(0xff166edb)
                            : const Color(0xff3b3b45),
                      ),
                      onPressed: () {
                        if (hid >= 0) {
                          keys.press(hid);
                        } else {
                          keys.toggle(-hid);
                          changed();
                        }
                      },
                      child: Text(label, style: const TextStyle(fontSize: 13)),
                    ),
                  );
                },
              ),
            ),
            IconButton(
              tooltip: '收起键盘',
              color: Colors.white,
              onPressed: close,
              icon: const Icon(Icons.keyboard_hide_outlined, size: 21),
            ),
          ],
        ),
      ),
    );
  }
}

class RemoteTextInput extends StatefulWidget {
  const RemoteTextInput({
    super.key,
    required this.keys,
    required this.focus,
    required this.onUnsupported,
  });
  final RemoteKeys keys;
  final FocusNode focus;
  final ValueChanged<String> onUnsupported;

  @override
  State<RemoteTextInput> createState() => _RemoteTextInputState();
}

class _RemoteTextInputState extends State<RemoteTextInput> {
  static const _sentinel = '\u200b';
  final _controller = TextEditingController.fromValue(
    const TextEditingValue(
      text: _sentinel,
      selection: TextSelection.collapsed(offset: 1),
    ),
  );
  final _hardware = <int>{};

  @override
  void initState() {
    super.initState();
    _controller.addListener(_onText);
  }

  void _onText() {
    final value = _controller.value;
    if (value.composing.isValid && !value.composing.isCollapsed) return;
    if (value.text == _sentinel) return;
    _controller.value = const TextEditingValue(
      text: _sentinel,
      selection: TextSelection.collapsed(offset: 1),
    );
    if (value.text.isEmpty) {
      widget.keys.press(0x2a);
      return;
    }
    final text = value.text.replaceAll(_sentinel, '');
    final unsupported = StringBuffer();
    for (final rune in text.runes) {
      final character = String.fromCharCode(rune);
      if (!widget.keys.type(character)) unsupported.write(character);
    }
    if (unsupported.isNotEmpty) widget.onUnsupported(unsupported.toString());
  }

  KeyEventResult _hardwareKey(FocusNode node, KeyEvent event) {
    final usage = event.physicalKey.usbHidUsage;
    if (usage >> 16 != 0x07) return KeyEventResult.ignored;
    final hid = usage & 0xffff;
    final down = event is! KeyUpEvent;
    if (down) {
      _hardware.add(hid);
    } else {
      _hardware.remove(hid);
    }
    widget.keys.session.key(hid, down, widget.keys.modifiers);
    return KeyEventResult.handled;
  }

  @override
  void dispose() {
    for (final hid in _hardware) {
      widget.keys.session.key(hid, false, widget.keys.modifiers);
    }
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => IgnorePointer(
    child: ExcludeSemantics(
      child: Opacity(
        opacity: 0,
        child: SizedBox.square(
          dimension: 1,
          child: Focus(
            canRequestFocus: false,
            onKeyEvent: _hardwareKey,
            child: EditableText(
              controller: _controller,
              focusNode: widget.focus,
              onTapOutside: (_) {},
              keyboardType: TextInputType.multiline,
              keyboardAppearance: Theme.of(context).brightness,
              textInputAction: TextInputAction.newline,
              maxLines: null,
              autocorrect: false,
              enableSuggestions: false,
              style: const TextStyle(fontSize: 14),
              cursorColor: Colors.transparent,
              backgroundCursorColor: Colors.transparent,
            ),
          ),
        ),
      ),
    ),
  );
}
