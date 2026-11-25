import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../activity_history.dart';
import 'common.dart';

class TransfersPage extends StatelessWidget {
  const TransfersPage({super.key, required this.history});
  final ActivityHistory history;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: history,
    builder: (context, _) => ListView(
      padding: EdgeInsets.fromLTRB(
        20,
        22,
        20,
        26 + MediaQuery.paddingOf(context).bottom,
      ),
      children: [
        const PageTitle('传输'),
        const SizedBox(height: 23),
        GroupCard(
          children: [
            Semantics(
              enabled: false,
              button: true,
              child: KRow(
                title: '文件传输',
                subtitle: '在手机和电脑之间发送文件',
                dimmed: true,
                leading: _icon(context, CupertinoIcons.folder, disabled: true),
                trailing: DecoratedBox(
                  decoration: BoxDecoration(
                    color: context.recessed,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 9,
                      vertical: 5,
                    ),
                    child: Text(
                      '第 2 期',
                      style: TextStyle(color: context.muted, fontSize: 12),
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 22),
        GroupCard(
          title: '最近连接',
          children: [
            if (history.connections.isEmpty)
              const _Empty(CupertinoIcons.clock, '暂无连接记录', '连接电脑后会在这里显示时间和时长。')
            else
              for (final item in history.connections)
                KRow(
                  leading: _icon(context, CupertinoIcons.desktopcomputer),
                  title: item.computer,
                  subtitle:
                      '${_time(context, item.start)} · ${_duration(item.duration)}',
                  trailing: Text(
                    item.path,
                    style: TextStyle(color: context.action, fontSize: 13),
                  ),
                ),
          ],
        ),
        const SizedBox(height: 22),
        GroupCard(
          title: '剪贴板同步',
          children: [
            if (history.clipboard.isEmpty)
              const _Empty(
                CupertinoIcons.doc_on_clipboard,
                '暂无同步记录',
                '在远程工具中同步剪贴板后会显示文字片段。',
              )
            else
              for (final item in history.clipboard)
                KRow(
                  title: item.text.replaceAll(RegExp(r'\s+'), ' '),
                  subtitle: '${item.computer} · ${_time(context, item.time)}',
                  leading: _icon(context, CupertinoIcons.doc_on_clipboard),
                  trailing: Icon(
                    CupertinoIcons.doc_on_doc,
                    color: context.action,
                    size: 17,
                  ),
                  onTap: () async {
                    try {
                      await Clipboard.setData(ClipboardData(text: item.text));
                      if (context.mounted) {
                        KNotice.show(context, '已复制', kind: KNoticeKind.success);
                      }
                    } on PlatformException {
                      if (context.mounted) {
                        KNotice.show(context, '无法复制', kind: KNoticeKind.error);
                      }
                    }
                  },
                ),
          ],
        ),
      ],
    ),
  );

  Widget _icon(BuildContext context, IconData icon, {bool disabled = false}) =>
      Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(
          color: disabled ? context.recessed : context.blueTint,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Icon(
          icon,
          size: 19,
          color: disabled ? context.muted : context.action,
        ),
      );

  String _time(BuildContext context, DateTime date) {
    date = date.toLocal();
    final time = MaterialLocalizations.of(context)
        .formatTimeOfDay(TimeOfDay.fromDateTime(date));
    return '${date.month}月${date.day}日 $time';
  }

  String _duration(Duration duration) => duration.inHours > 0
      ? '${duration.inHours} 小时 ${duration.inMinutes.remainder(60)} 分'
      : duration.inMinutes > 0
      ? '${duration.inMinutes} 分 ${duration.inSeconds.remainder(60)} 秒'
      : '${duration.inSeconds} 秒';
}

class _Empty extends StatelessWidget {
  const _Empty(this.icon, this.title, this.detail);
  final IconData icon;
  final String title;
  final String detail;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 22),
    child: Column(
      children: [
        Icon(icon, color: context.action, size: 25),
        const SizedBox(height: 9),
        Text(
          title,
          style: TextStyle(color: context.ink, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 4),
        Text(
          detail,
          textAlign: TextAlign.center,
          style: TextStyle(color: context.muted, fontSize: 13),
        ),
      ],
    ),
  );
}
