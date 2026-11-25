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
          padding: const EdgeInsets.fromLTRB(20, 1, 20, 25),
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                IconButton(
                  key: const ValueKey('scan'),
                  tooltip: '扫码配对',
                  icon: const Icon(CupertinoIcons.viewfinder, size: 25),
                  color: context.action,
                  onPressed: widget.scan,
                ),
                IconButton(
                  tooltip: '添加电脑',
                  icon: const Icon(CupertinoIcons.add, size: 27),
                  color: context.action,
                  onPressed: widget.add,
                ),
              ],
            ),
            const PageTitle('设备'),
            const SizedBox(height: 13),
            TextField(
              controller: _search,
              textInputAction: TextInputAction.search,
              decoration: InputDecoration(
                contentPadding: const EdgeInsets.symmetric(vertical: 10),
                filled: true,
                fillColor: context.recessed,
                prefixIcon: Icon(
                  CupertinoIcons.search,
                  size: 18,
                  color: context.muted,
                ),
                suffixIcon: query.isEmpty
                    ? null
                    : IconButton(
                        tooltip: '清除搜索',
                        icon: const Icon(
                          CupertinoIcons.clear_circled_solid,
                          size: 17,
                        ),
                        onPressed: _search.clear,
                      ),
                hintText: '搜索电脑',
                hintStyle: TextStyle(color: context.muted, fontSize: 15),
                border: OutlineInputBorder(
                  borderSide: BorderSide.none,
                  borderRadius: BorderRadius.circular(12),
                ),
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
          SlidableAction(
            padding: EdgeInsets.zero,
            onPressed: online ? (_) => view() : null,
            icon: CupertinoIcons.eye,
            label: '仅查看',
            backgroundColor: online
                ? const Color(0xff1566d4)
                : const Color(0xff666670),
            foregroundColor: Colors.white,
          ),
          SlidableAction(
            padding: EdgeInsets.zero,
            onPressed: null,
            icon: CupertinoIcons.folder,
            label: '传文件',
            backgroundColor: const Color(0xff666670),
            foregroundColor: Colors.white,
          ),
          SlidableAction(
            padding: EdgeInsets.zero,
            onPressed: (_) => rename(),
            icon: CupertinoIcons.pencil,
            label: '改名',
            backgroundColor: const Color(0xff454550),
            foregroundColor: Colors.white,
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
              TextButton(
                key: ValueKey('connect-${computer.id}'),
                style: TextButton.styleFrom(
                  backgroundColor: context.blueTint,
                  foregroundColor: context.action,
                  visualDensity: VisualDensity.compact,
                  minimumSize: const Size(65, 36),
                  padding: const EdgeInsets.symmetric(horizontal: 13),
                  textStyle: const TextStyle(fontWeight: FontWeight.w700),
                ),
                onPressed: connect,
                child: const Text('连接'),
              )
            else
              IconButton(
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

class LanSignIn extends StatefulWidget {
  const LanSignIn({super.key});

  @override
  State<LanSignIn> createState() => _LanSignInState();
}

class _LanSignInState extends State<LanSignIn> {
  final _form = GlobalKey<FormState>();
  final _address = TextEditingController();
  final _password = TextEditingController();
  bool _obscure = true;

  @override
  void dispose() {
    _address.dispose();
    _password.dispose();
    super.dispose();
  }

  void _submit() {
    if (_form.currentState!.validate()) {
      Navigator.pop(context, (
        address: _address.text.trim(),
        password: _password.text,
      ));
    }
  }

  @override
  Widget build(BuildContext context) => Form(
    key: _form,
    child: Column(
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
        TextFormField(
          autofocus: true,
          controller: _address,
          keyboardType: TextInputType.url,
          textInputAction: TextInputAction.next,
          autocorrect: false,
          decoration: const InputDecoration(
            labelText: '电脑地址',
            hintText: '192.168.1.10:21118',
            border: OutlineInputBorder(),
          ),
          validator: (value) {
            final endpoint = Uri.tryParse('tcp://${value?.trim() ?? ''}');
            return endpoint == null ||
                    endpoint.host.isEmpty ||
                    endpoint.path.isNotEmpty ||
                    endpoint.userInfo.isNotEmpty ||
                    endpoint.hasQuery ||
                    endpoint.hasFragment ||
                    (endpoint.hasPort &&
                        (endpoint.port < 1 || endpoint.port > 65535))
                ? '请输入有效的电脑地址，可附加端口'
                : null;
          },
        ),
        const SizedBox(height: 12),
        TextFormField(
          controller: _password,
          obscureText: _obscure,
          textInputAction: TextInputAction.done,
          autocorrect: false,
          enableSuggestions: false,
          onFieldSubmitted: (_) => _submit(),
          decoration: InputDecoration(
            labelText: '连接密码',
            border: const OutlineInputBorder(),
            suffixIcon: IconButton(
              tooltip: _obscure ? '显示密码' : '隐藏密码',
              onPressed: () => setState(() => _obscure = !_obscure),
              icon: Icon(
                _obscure ? CupertinoIcons.eye : CupertinoIcons.eye_slash,
              ),
            ),
          ),
          validator: (value) =>
              value == null || value.isEmpty ? '请输入连接密码' : null,
        ),
        const SizedBox(height: 16),
        FilledButton(onPressed: _submit, child: const Text('连接')),
      ],
    ),
  );
}
