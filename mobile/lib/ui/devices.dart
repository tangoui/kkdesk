import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_slidable/flutter_slidable.dart';

import '../mobile_services.dart';
import 'common.dart';

class DevicesPage extends StatefulWidget {
  const DevicesPage({
    super.key,
    required this.devices,
    required this.scan,
    required this.add,
    required this.connect,
  });

  final DeviceRepository devices;
  final VoidCallback scan;
  final VoidCallback add;
  final void Function(Computer computer, {bool viewOnly}) connect;

  @override
  State<DevicesPage> createState() => _DevicesPageState();
}

class _DevicesPageState extends State<DevicesPage> {
  final _search = TextEditingController();

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([widget.devices, _search]),
    builder: (context, _) {
      final query = _search.text.toLowerCase().trim();
      final matches = widget.devices.computers.where(
        (computer) =>
            computer.name.toLowerCase().contains(query) ||
            computer.os.label.toLowerCase().contains(query),
      );
      final online = matches.where((computer) => computer.online).toList();
      final offline = matches.where((computer) => !computer.online).toList();
      return SlidableAutoCloseBehavior(
        child: ListView(
          keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
          padding: EdgeInsets.fromLTRB(
            20,
            1,
            20,
            25 + MediaQuery.paddingOf(context).bottom,
          ),
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                const BrandMark(size: 27),
                const Spacer(),
                KIconButton(
                  key: const ValueKey('scan'),
                  tooltip: '扫码配对',
                  icon: const Icon(CupertinoIcons.viewfinder, size: 25),
                  color: context.action,
                  onPressed: widget.scan,
                ),
                KIconButton(
                  tooltip: '添加电脑',
                  icon: const Icon(CupertinoIcons.add, size: 27),
                  color: context.action,
                  onPressed: widget.add,
                ),
              ],
            ),
            const PageTitle('设备'),
            const SizedBox(height: 13),
            KInput(
              controller: _search,
              textInputAction: TextInputAction.search,
              hint: '搜索电脑',
              clearLabel: '清除搜索',
              leading: Icon(
                CupertinoIcons.search,
                size: 18,
                color: context.muted,
              ),
            ),
            const SizedBox(height: 23),
            if (online.isNotEmpty) _group('在线', online),
            if (online.isNotEmpty && offline.isNotEmpty)
              const SizedBox(height: 24),
            if (offline.isNotEmpty) _group('离线', offline),
            if (online.isEmpty && offline.isEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 35),
                child: Text(
                  query.isEmpty ? '暂无设备，扫码或添加电脑' : '未找到匹配的电脑',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: context.muted),
                ),
              ),
          ],
        ),
      );
    },
  );

  Widget _group(String name, List<Computer> computers) => GroupCard(
    title: '$name · ${computers.length}',
    children: [
      for (final computer in computers)
        _DeviceRow(
          key: ValueKey(computer.id),
          computer: computer,
          connect: () => widget.connect(computer),
          view: () => widget.connect(computer, viewOnly: true),
          rename: () => renameComputer(context, widget.devices, computer),
        ),
    ],
  );
}

class _DeviceRow extends StatelessWidget {
  const _DeviceRow({
    super.key,
    required this.computer,
    required this.connect,
    required this.view,
    required this.rename,
  });

  final Computer computer;
  final VoidCallback connect;
  final VoidCallback view;
  final VoidCallback rename;

  @override
  Widget build(BuildContext context) {
    final online = computer.online;
    final metadata = online
        ? '${computer.path.label} · ${computer.latency} ms'
        : '${computer.lastSeen ?? '离线'}${computer.id == 'home' ? ' · 每次需确认' : ''}';
    final dot = !online
        ? context.muted
        : computer.path == ConnectionPath.direct
        ? green
        : amber;
    return Slidable(
      groupTag: 'devices',
      endActionPane: ActionPane(
        motion: const DrawerMotion(),
        extentRatio: .65,
        children: [
          _SwipeAction(
            label: '仅查看',
            icon: CupertinoIcons.eye,
            color: blue,
            onTap: online ? view : null,
          ),
          const _SwipeAction(
            label: '传文件',
            icon: CupertinoIcons.folder,
            color: Color(0xff666670),
          ),
          _SwipeAction(
            label: '改名',
            icon: CupertinoIcons.pencil,
            color: const Color(0xff454550),
            onTap: rename,
          ),
        ],
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(13, 7, 10, 7),
        child: Row(
          children: [
            OSIcon(computer.os, dim: !online),
            const SizedBox(width: 13),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    computer.name,
                    style: TextStyle(
                      fontWeight: FontWeight.w700,
                      color: context.ink,
                      fontSize: 16,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      Container(
                        width: 7,
                        height: 7,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          color: dot,
                        ),
                      ),
                      const SizedBox(width: 6),
                      Flexible(
                        child: Text(
                          metadata,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(color: context.muted, fontSize: 12),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            if (online)
              KButton(
                key: ValueKey('connect-${computer.id}'),
                label: '连接',
                variant: KButtonVariant.secondary,
                compact: true,
                onTap: connect,
              )
            else
              KIconButton(
                onPressed: rename,
                tooltip: '更改名称',
                icon: Icon(
                  CupertinoIcons.chevron_right,
                  size: 16,
                  color: context.muted,
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _SwipeAction extends StatelessWidget {
  const _SwipeAction({
    required this.label,
    required this.icon,
    required this.color,
    this.onTap,
  });
  final String label;
  final IconData icon;
  final Color color;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) => Expanded(
    child: ColoredBox(
      color: onTap == null ? const Color(0xff666670) : color,
      child: KPress(
        onTap: onTap == null
            ? null
            : () {
                Slidable.of(context)?.close();
                onTap!();
              },
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                icon,
                color: onTap == null ? Colors.white54 : Colors.white,
                size: 19,
              ),
              const SizedBox(height: 4),
              Text(
                label,
                style: TextStyle(
                  color: onTap == null ? Colors.white54 : Colors.white,
                  fontSize: 11,
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class LanSignIn extends StatefulWidget {
  const LanSignIn({super.key});

  @override
  State<LanSignIn> createState() => _LanSignInState();
}

class _LanSignInState extends State<LanSignIn> {
  final _passwordFocus = FocusNode();
  String? _addressError;
  String? _passwordError;
  final _address = TextEditingController();
  final _password = TextEditingController();
  bool _obscure = true;

  @override
  void dispose() {
    _address.dispose();
    _password.dispose();
    _passwordFocus.dispose();
    super.dispose();
  }

  void _submit() {
    final address = _address.text.trim();
    final endpoint = Uri.tryParse('tcp://$address');
    final validAddress =
        endpoint != null &&
        endpoint.host.isNotEmpty &&
        endpoint.path.isEmpty &&
        endpoint.userInfo.isEmpty &&
        !endpoint.hasQuery &&
        !endpoint.hasFragment &&
        (!endpoint.hasPort || (endpoint.port >= 1 && endpoint.port <= 65535));
    setState(() {
      _addressError = validAddress ? null : '请输入有效的电脑地址，可附加端口';
      _passwordError = _password.text.isEmpty ? '请输入连接密码' : null;
    });
    if (_addressError == null && _passwordError == null) {
      Navigator.pop(context, (address: address, password: _password.text));
    }
  }

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text(
        '局域网连接',
        textAlign: TextAlign.center,
        style: Theme.of(context).textTheme.titleLarge,
      ),
      const SizedBox(height: 8),
      Text(
        '输入电脑端显示的地址和连接密码',
        textAlign: TextAlign.center,
        style: TextStyle(color: context.muted),
      ),
      const SizedBox(height: 20),
      KInput(
        controller: _address,
        label: '电脑地址',
        hint: '192.168.1.10:21118',
        error: _addressError,
        keyboardType: TextInputType.url,
        textInputAction: TextInputAction.next,
        onSubmitted: (_) => _passwordFocus.requestFocus(),
        onChanged: (_) {
          if (_addressError != null) setState(() => _addressError = null);
        },
      ),
      const SizedBox(height: 12),
      KInput(
        controller: _password,
        focusNode: _passwordFocus,
        label: '连接密码',
        error: _passwordError,
        obscureText: _obscure,
        textInputAction: TextInputAction.done,
        onSubmitted: (_) => _submit(),
        onChanged: (_) {
          if (_passwordError != null) setState(() => _passwordError = null);
        },
        trailing: KIconButton(
          tooltip: _obscure ? '显示密码' : '隐藏密码',
          onPressed: () => setState(() => _obscure = !_obscure),
          icon: Icon(_obscure ? CupertinoIcons.eye : CupertinoIcons.eye_slash),
        ),
      ),
      const SizedBox(height: 16),
      KButton(label: '连接', onTap: _submit),
      KButton(
        label: '取消',
        variant: KButtonVariant.text,
        onTap: () => Navigator.pop(context),
      ),
    ],
  );
}
