import 'dart:io';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:open_filex/open_filex.dart';
import 'package:share_plus/share_plus.dart';

import '../../files/file_transfer_service.dart';
import '../common.dart';
import 'transfer_center.dart';

class FileQueuePage extends StatelessWidget {
  const FileQueuePage({super.key, required this.center});
  final TransferCenter center;

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: context.background,
    body: SafeArea(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const KPageHeader('传输队列'),
          Expanded(
            child: ListenableBuilder(
              listenable: center,
              builder: (context, _) => ListView(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 20),
                children: [
                  ActiveTransferGroup(center: center),
                  const SizedBox(height: 20),
                  TransferHistoryGroup(center: center),
                ],
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

class ActiveTransferGroup extends StatelessWidget {
  const ActiveTransferGroup({super.key, required this.center});
  final TransferCenter center;

  @override
  Widget build(BuildContext context) {
    final active = center.active;
    return GroupCard(
      title: '进行中',
      children: [
        if (active.isEmpty)
          const KRow(title: '暂无进行中的传输')
        else
          for (final transfer in active) _ActiveRow(transfer),
      ],
    );
  }
}

class _ActiveRow extends StatelessWidget {
  const _ActiveRow(this.transfer);
  final ActiveTransfer transfer;

  @override
  Widget build(BuildContext context) {
    final snapshot = transfer.snapshot;
    final queued = snapshot.status == FileTransferStatus.queued;
    final total = snapshot.totalBytes;
    final fraction = total != null && total > 0
        ? (snapshot.bytes / total).clamp(0.0, 1.0)
        : null;
    final remaining = snapshot.remaining;
    final direction = _direction(snapshot.direction);
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 11, 14, 12),
      child: Row(
        children: [
          _DirectionIcon(snapshot.direction, context.action),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              spacing: 6,
              children: [
                Text(
                  transfer.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                Text(
                  queued
                      ? '$direction · 等待中'
                      : [
                          direction,
                          '${byteCount(snapshot.bytes)}${total == null ? '' : ' / ${byteCount(total)}'}',
                          if (snapshot.bytesPerSecond > 0)
                            '${byteCount(snapshot.bytesPerSecond)}/秒',
                          if (remaining != null) '剩余 ${_duration(remaining)}',
                        ].join(' · '),
                  style: TextStyle(color: context.muted, fontSize: 12),
                ),
                Semantics(
                  label: '$direction进度',
                  value: fraction == null
                      ? '等待'
                      : '${(fraction * 100).round()}%',
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(20),
                    child: LinearProgressIndicator(
                      value: queued ? 0 : fraction,
                      color: context.action,
                      minHeight: 4,
                      backgroundColor: context.recessed,
                    ),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          KButton(
            label: '取消',
            compact: true,
            variant: KButtonVariant.text,
            onTap: transfer.cancel,
          ),
        ],
      ),
    );
  }

  String _duration(Duration time) => time.inHours > 0
      ? '${time.inHours} 小时 ${time.inMinutes % 60} 分'
      : time.inMinutes > 0
      ? '${time.inMinutes} 分 ${time.inSeconds % 60} 秒'
      : '${time.inSeconds} 秒';
}

class TransferHistoryGroup extends StatelessWidget {
  const TransferHistoryGroup({super.key, required this.center});
  final TransferCenter center;

  @override
  Widget build(BuildContext context) => GroupCard(
    title: '文件传输记录',
    children: [
      if (center.records.isEmpty)
        const KRow(title: '暂无文件传输记录')
      else
        for (final item in center.records)
          _RecordRow(item: item, center: center),
    ],
  );
}

class _RecordRow extends StatefulWidget {
  const _RecordRow({required this.item, required this.center});
  final TransferRecord item;
  final TransferCenter center;

  @override
  State<_RecordRow> createState() => _RecordRowState();
}

class _RecordRowState extends State<_RecordRow> {
  bool _retrying = false;

  Future<void> _retry() async {
    if (_retrying) return;
    setState(() => _retrying = true);
    try {
      await widget.item.retry!();
    } catch (error) {
      if (mounted) {
        KNotice.show(context, transferError(error), kind: KNoticeKind.error);
      }
    } finally {
      if (mounted) setState(() => _retrying = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final item = widget.item;
    final (state, color) = switch (item.status) {
      FileTransferStatus.completed => ('已完成', green),
      FileTransferStatus.failed => ('失败', red),
      FileTransferStatus.partial => ('部分完成', amber),
      FileTransferStatus.canceled => ('已取消', context.muted),
      _ => ('等待中', context.muted),
    };
    final time = item.time.toLocal();
    final labels = MaterialLocalizations.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        KRow(
          leading: _DirectionIcon(item.direction, color),
          title: item.name,
          subtitle:
              '${_direction(item.direction)} · $state · ${byteCount(item.bytes)} · ${labels.formatCompactDate(time)} ${labels.formatTimeOfDay(TimeOfDay.fromDateTime(time))}',
          trailing: item.localFolder != null
              ? KIconButton(
                  tooltip: '查看已下载文件',
                  icon: const Icon(CupertinoIcons.folder_open),
                  onPressed: () =>
                      _openDownloads(context, widget.center, item.localFolder!),
                )
              : null,
        ),
        if (item.detail != null || item.retry != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(14, 0, 10, 8),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    item.detail ?? '',
                    style: TextStyle(color: color, fontSize: 12),
                  ),
                ),
                if (item.retry != null)
                  KButton(
                    label: '重试',
                    compact: true,
                    variant: KButtonVariant.text,
                    onTap: _retrying ? null : _retry,
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

Future<void> _openDownloads(
  BuildContext context,
  TransferCenter center,
  String folder,
) async {
  if (!RegExp(r'^传输-[^/\\]+$').hasMatch(folder)) return;
  try {
    final root = await center.downloadRoot();
    final directory = Directory('${root.path}/$folder');
    if (!await directory.exists()) throw const FileTransferError('not_found');
    if (!context.mounted) return;
    await Navigator.push<void>(
      context,
      MaterialPageRoute(builder: (_) => _LocalDownloads(directory: directory)),
    );
  } catch (error) {
    if (context.mounted) {
      KNotice.show(context, transferError(error), kind: KNoticeKind.error);
    }
  }
}

class _LocalDownloads extends StatefulWidget {
  const _LocalDownloads({required this.directory});
  final Directory directory;

  @override
  State<_LocalDownloads> createState() => _LocalDownloadsState();
}

class _LocalDownloadsState extends State<_LocalDownloads> {
  late final _files = widget.directory
      .list(recursive: true, followLinks: false)
      .where((entry) => entry is File)
      .cast<File>()
      .toList();

  Future<void> _options(BuildContext context, File file) async {
    final box = context.findRenderObject()! as RenderBox;
    final origin = box.localToGlobal(Offset.zero) & box.size;
    final choice = await appSheet<bool>(
      context,
      (context) => KOptionGroup(
        title: fileBaseName(file.path),
        children: [
          KRow(
            title: '分享',
            onTap: () => Navigator.pop(context, true),
            leading: const Icon(CupertinoIcons.share),
          ),
          KRow(
            title: '用其他应用打开',
            onTap: () => Navigator.pop(context, false),
            leading: const Icon(CupertinoIcons.arrow_up_right_square),
          ),
        ],
      ),
      grouped: true,
    );
    if (choice == null || !context.mounted) return;
    try {
      if (choice) {
        await SharePlus.instance.share(
          ShareParams(files: [XFile(file.path)], sharePositionOrigin: origin),
        );
      } else {
        final result = await OpenFilex.open(file.path);
        if (result.type != ResultType.done && context.mounted) {
          KNotice.show(context, '找不到可以打开此文件的应用', kind: KNoticeKind.error);
        }
      }
    } catch (_) {
      if (context.mounted) {
        KNotice.show(context, '无法打开或分享此文件', kind: KNoticeKind.error);
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: context.background,
    body: SafeArea(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const KPageHeader('已下载文件'),
          Expanded(
            child: FutureBuilder(
              future: _files,
              builder: (context, snapshot) {
                if (snapshot.hasError) {
                  return const Center(child: Text('无法读取下载目录'));
                }
                if (!snapshot.hasData) return const Center(child: KLoader());
                final files = snapshot.data!;
                if (files.isEmpty) return const Center(child: Text('下载的文件夹为空'));
                return ListView(
                  padding: const EdgeInsets.all(16),
                  children: [
                    GroupCard(
                      children: [
                        for (final file in files)
                          KRow(
                            title: file.path.substring(
                              widget.directory.path.length + 1,
                            ),
                            leading: Icon(
                              CupertinoIcons.doc,
                              color: context.action,
                            ),
                            onTap: () => _options(context, file),
                            chevron: true,
                          ),
                      ],
                    ),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    ),
  );
}

String _direction(FileTransferDirection direction) =>
    direction == FileTransferDirection.upload ? '上传' : '下载';

class _DirectionIcon extends StatelessWidget {
  const _DirectionIcon(this.direction, this.color);
  final FileTransferDirection direction;
  final Color color;
  @override
  Widget build(BuildContext context) => Container(
    width: 36,
    height: 36,
    decoration: BoxDecoration(
      color: color.withValues(alpha: .15),
      borderRadius: BorderRadius.circular(10),
    ),
    child: Icon(
      direction == FileTransferDirection.upload
          ? CupertinoIcons.arrow_up
          : CupertinoIcons.arrow_down,
      color: color,
      size: 19,
    ),
  );
}
