import 'package:file_picker/file_picker.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../files/file_transfer_service.dart';
import '../common.dart';
import 'media_upload.dart';
import 'transfer_center.dart';
import 'transfer_rows.dart';

class FileBrowser extends StatefulWidget {
  const FileBrowser({super.key, required this.connect, required this.center});
  final Future<TransferConnection> Function() connect;
  final TransferCenter center;

  @override
  State<FileBrowser> createState() => _FileBrowserState();
}

class _FileBrowserState extends State<FileBrowser> {
  TransferConnection? _connection;
  RemoteHome? _home;
  RemoteListing? _listing;
  final _selected = <String>{};
  bool _busy = false;
  bool _selecting = false;
  bool _submitting = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    final connection = _connection;
    if (connection != null) widget.center.closeBrowser(connection);
    super.dispose();
  }

  Future<void> _load([String? path]) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      var connection = _connection;
      if (connection == null || connection.closed) {
        final previous = connection;
        final center = widget.center;
        connection = await widget.connect();
        center.openBrowser(connection);
        if (!mounted) {
          center.closeBrowser(connection);
          return;
        }
        if (previous != null) center.closeBrowser(previous);
        _connection = connection;
        _home = null;
      }
      final service = connection.service;
      final home = _home ??= await service.home();
      final listing = await service.list(path ?? home.directory);
      if (!mounted) return;
      setState(() {
        if (_listing?.path != listing.path) {
          _selected.clear();
          _selecting = false;
        } else {
          _selected.retainAll(listing.entries.map((entry) => entry.path));
        }
        _listing = listing;
      });
    } catch (error) {
      if (mounted) setState(() => _error = transferError(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _volumes() async {
    final home = _home!;
    final selection = await appSheet<({String? path, bool manual})>(
      context,
      (context) => KOptionGroup(
        title: '位置',
        children: [
          KRow(
            leading: Icon(CupertinoIcons.house, color: context.action),
            title: '主目录',
            subtitle: home.directory,
            onTap: () =>
                Navigator.pop(context, (path: home.directory, manual: false)),
          ),
          for (final volume in home.volumes)
            KRow(
              leading: Icon(
                CupertinoIcons.device_desktop,
                color: context.action,
              ),
              title: volume,
              onTap: () =>
                  Navigator.pop(context, (path: volume, manual: false)),
            ),
          KRow(
            title: '前往路径',
            leading: Icon(CupertinoIcons.arrow_right, color: context.action),
            onTap: () => Navigator.pop(context, (path: null, manual: true)),
          ),
        ],
      ),
      grouped: true,
    );
    if (!mounted || selection == null) return;
    final path = selection.manual
        ? await editText(context, title: '前往路径', label: '电脑路径', initial: '')
        : selection.path;
    if (mounted && path != null) await _load(path);
  }

  Future<void> _mkdir() async {
    final location = _listing!.path;
    final separator = _home!.separator;
    final name = await editText(
      context,
      title: '新建文件夹',
      label: '文件夹名称',
      initial: '',
    );
    if (!mounted || name == null) return;
    if (name == '.' ||
        name == '..' ||
        name.contains(separator) ||
        name.contains('\u0000') ||
        (separator == '\\' && name.contains('/'))) {
      KNotice.show(context, '文件夹名称无效', kind: KNoticeKind.error);
      return;
    }
    await _submit(() async {
      await _connection!.service.mkdir(
        '${location.endsWith(separator) ? location : '$location$separator'}$name',
      );
      if (mounted) await _load(location);
    });
  }

  Future<void> _download() async {
    final connection = _connection!;
    final entries = _listing!.entries
        .where((entry) => _selected.contains(entry.path))
        .toList();
    await _submit(() async {
      for (final entry in entries) {
        await widget.center.download(connection, entry);
      }
      if (!mounted) return;
      setState(() {
        _selecting = false;
        _selected.clear();
      });
      _showQueue();
    });
  }

  Future<void> _upload() async {
    final connection = _connection!;
    final directory = _listing!.path;
    final photo = await appSheet<bool>(
      context,
      (context) => KOptionGroup(
        title: '上传到此文件夹',
        children: [
          KRow(
            title: '照片和视频',
            leading: Icon(CupertinoIcons.photo, color: context.action),
            onTap: () => Navigator.pop(context, true),
          ),
          KRow(
            title: '文件',
            leading: Icon(CupertinoIcons.doc, color: context.action),
            onTap: () => Navigator.pop(context, false),
          ),
        ],
      ),
      grouped: true,
    );
    if (photo == null || !mounted) return;
    await _submit(() async {
      final sources = photo
          ? await pickMediaForUpload()
          : [
              for (final file in await FilePicker.pickFiles())
                if (file.path != null)
                  UploadSource.file(
                    name: file.name,
                    size: await file.xFile.length(),
                    openRead: file.xFile.openRead,
                  ),
            ];
      if (!mounted || sources.isEmpty) return;
      for (final source in sources) {
        widget.center.upload(connection, source, directory);
      }
      _showQueue();
    });
  }

  Future<void> _submit(Future<void> Function() action) async {
    if (_submitting) return;
    setState(() => _submitting = true);
    try {
      await action();
    } catch (error) {
      if (mounted) {
        KNotice.show(context, transferError(error), kind: KNoticeKind.error);
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  void _showQueue() => Navigator.push<void>(
    context,
    MaterialPageRoute(builder: (_) => FileQueuePage(center: widget.center)),
  );

  void _toggle(RemoteEntry item) => setState(() {
    _selecting = true;
    if (!_selected.remove(item.path)) _selected.add(item.path);
  });

  @override
  Widget build(BuildContext context) {
    final listing = _listing;
    final canAct =
        listing != null &&
        !_busy &&
        !_submitting &&
        _connection?.closed != true;
    final entries = [...?listing?.entries]
      ..sort((a, b) {
        if (a.isDirectory != b.isDirectory) return a.isDirectory ? -1 : 1;
        return a.name.toLowerCase().compareTo(b.name.toLowerCase());
      });
    final labels = MaterialLocalizations.of(context);
    return Scaffold(
      backgroundColor: context.background,
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                KIconButton(
                  tooltip: '返回',
                  icon: const Icon(CupertinoIcons.chevron_left),
                  onPressed: () => Navigator.maybePop(context),
                ),
                const Spacer(),
                KIconButton(
                  tooltip: '位置',
                  icon: const Icon(CupertinoIcons.folder),
                  onPressed: !_busy && _home != null ? _volumes : null,
                ),
                KIconButton(
                  tooltip: '传输队列',
                  icon: const Icon(CupertinoIcons.arrow_up_arrow_down),
                  onPressed: _showQueue,
                ),
              ],
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
              child: Row(
                children: [
                  const Expanded(child: PageTitle('电脑文件')),
                  if (listing != null)
                    KButton(
                      label: _selecting ? '退出多选' : '多选',
                      compact: true,
                      variant: KButtonVariant.text,
                      onTap: !canAct
                          ? null
                          : () => setState(() {
                              _selecting = !_selecting;
                              _selected.clear();
                            }),
                    ),
                ],
              ),
            ),
            if (listing != null && _home != null)
              _Breadcrumb(
                path: listing.path,
                home: _home!,
                parent: listing.parent,
                onTap: _busy ? null : _load,
              ),
            if (_error case final error?)
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 5, 20, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(error, style: const TextStyle(color: red)),
                    ),
                    KButton(
                      label: '重试',
                      compact: true,
                      variant: KButtonVariant.secondary,
                      onTap: _busy ? null : () => _load(listing?.path),
                    ),
                  ],
                ),
              ),
            Expanded(
              child: listing == null && _busy
                  ? const Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        spacing: 10,
                        children: [KLoader(radius: 12), Text('正在读取电脑文件…')],
                      ),
                    )
                  : RefreshIndicator(
                      onRefresh: () => _load(listing?.path),
                      color: context.action,
                      backgroundColor: context.card,
                      child: ListView.builder(
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(16, 6, 16, 12),
                        itemCount: entries.isEmpty
                            ? 1
                            : entries.length +
                                  (listing?.truncated == true ? 1 : 0),
                        itemBuilder: (context, index) {
                          if (entries.isEmpty) {
                            return Padding(
                              padding: const EdgeInsets.all(24),
                              child: Text(
                                listing == null ? '尚未读取到文件' : '此文件夹为空',
                                textAlign: TextAlign.center,
                                style: TextStyle(color: context.muted),
                              ),
                            );
                          }
                          if (index == entries.length) {
                            return Padding(
                              padding: const EdgeInsets.all(16),
                              child: Text(
                                '电脑只返回了部分项目，可打开子文件夹查看。',
                                style: TextStyle(color: context.muted),
                              ),
                            );
                          }
                          final item = entries[index];
                          final time = item.modified.toLocal();
                          final modified =
                              '${labels.formatCompactDate(time)} ${labels.formatTimeOfDay(TimeOfDay.fromDateTime(time))}';
                          final selected = _selected.contains(item.path);
                          return DecoratedBox(
                            decoration: BoxDecoration(
                              color: selected ? context.blueTint : context.card,
                              borderRadius: BorderRadius.vertical(
                                top: index == 0
                                    ? const Radius.circular(15)
                                    : Radius.zero,
                                bottom: index == entries.length - 1
                                    ? const Radius.circular(15)
                                    : Radius.zero,
                              ),
                            ),
                            child: Column(
                              children: [
                                GestureDetector(
                                  onLongPress: canAct && !item.isLink
                                      ? () => _toggle(item)
                                      : null,
                                  child: KRow(
                                    title: item.name,
                                    subtitle: item.isLink
                                        ? '链接 · $modified'
                                        : item.isDirectory
                                        ? modified
                                        : '${byteCount(item.size)} · $modified',
                                    leading: Icon(
                                      item.isDirectory
                                          ? CupertinoIcons.folder_fill
                                          : CupertinoIcons.doc,
                                      color: item.isDirectory
                                          ? context.action
                                          : context.muted,
                                      size: 25,
                                    ),
                                    trailing: _selecting
                                        ? Icon(
                                            selected
                                                ? CupertinoIcons
                                                      .check_mark_circled_solid
                                                : CupertinoIcons.circle,
                                            color: selected
                                                ? context.action
                                                : context.muted,
                                          )
                                        : null,
                                    chevron:
                                        item.isDirectory &&
                                        !_selecting &&
                                        !item.isLink,
                                    dimmed: item.isLink,
                                    onTap: !canAct || item.isLink
                                        ? null
                                        : () {
                                            if (item.isDirectory &&
                                                !_selecting) {
                                              _load(item.path);
                                            } else {
                                              _toggle(item);
                                            }
                                          },
                                  ),
                                ),
                                if (index < entries.length - 1)
                                  const KRule(indent: 50),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
            ),
            if (listing != null)
              DecoratedBox(
                decoration: BoxDecoration(color: context.card),
                child: Column(
                  children: [
                    const KRule(),
                    Padding(
                      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
                      child: Row(
                        spacing: 8,
                        children: [
                          KIconButton(
                            tooltip: '新建文件夹',
                            icon: const Icon(CupertinoIcons.folder_badge_plus),
                            onPressed: canAct ? _mkdir : null,
                          ),
                          Expanded(
                            child: KButton(
                              label: '上传',
                              variant: KButtonVariant.secondary,
                              onTap: canAct ? _upload : null,
                            ),
                          ),
                          Expanded(
                            flex: 2,
                            child: KButton(
                              label: _selected.isEmpty
                                  ? '下载到手机'
                                  : '下载到手机（${_selected.length}）',
                              onTap: canAct && _selected.isNotEmpty
                                  ? _download
                                  : null,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _Breadcrumb extends StatelessWidget {
  const _Breadcrumb({
    required this.path,
    required this.home,
    required this.parent,
    required this.onTap,
  });
  final String path;
  final RemoteHome home;
  final String parent;
  final ValueChanged<String>? onTap;

  @override
  Widget build(BuildContext context) {
    final separator = home.separator;
    final volume = home.volumes
        .where((item) => path.toLowerCase().startsWith(item.toLowerCase()))
        .firstOrNull;
    final root = volume ?? (path.startsWith(separator) ? separator : '');
    final pieces = path
        .substring(root.length)
        .split(separator)
        .where((part) => part.isNotEmpty);
    var prefix = root;
    final crumbs = <(String, String)>[
      if (root.isNotEmpty) (root, root),
      for (final part in pieces)
        (
          part,
          prefix = (prefix.isEmpty || prefix.endsWith(separator))
              ? '$prefix$part'
              : '$prefix$separator$part',
        ),
    ];
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 5),
      child: Row(
        children: [
          KIconButton(
            tooltip: '主目录',
            extent: 39,
            icon: const Icon(CupertinoIcons.house, size: 19),
            onPressed: onTap == null ? null : () => onTap!(home.directory),
          ),
          if (parent.isNotEmpty && parent != path)
            KIconButton(
              tooltip: '上一级',
              extent: 36,
              icon: const Icon(CupertinoIcons.arrow_up, size: 18),
              onPressed: onTap == null ? null : () => onTap!(parent),
            ),
          Expanded(
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              reverse: true,
              child: Row(
                children: [
                  for (var index = 0; index < crumbs.length; index++) ...[
                    if (index > 0)
                      Icon(
                        CupertinoIcons.chevron_right,
                        size: 10,
                        color: context.muted,
                      ),
                    KButton(
                      label: crumbs[index].$1,
                      compact: true,
                      variant: KButtonVariant.text,
                      onTap: onTap == null
                          ? null
                          : () => onTap!(crumbs[index].$2),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
