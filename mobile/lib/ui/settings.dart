import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_slidable/flutter_slidable.dart';

import '../mobile_services.dart';
import 'common.dart';
import 'devices.dart';

class SettingsPage extends StatelessWidget {
  const SettingsPage({
    super.key,
    required this.services,
    required this.authenticate,
    required this.scan,
  });
  final MobileServices services;
  final Future<bool> Function(String reason) authenticate;
  final VoidCallback scan;

  @override
  Widget build(BuildContext context) {
    final settings = services.settings;
    return ListenableBuilder(
      listenable: Listenable.merge([settings, services.devices]),
      builder: (context, _) => ListView(
        padding: EdgeInsets.fromLTRB(
          20,
          22,
          20,
          26 + MediaQuery.paddingOf(context).bottom,
        ),
        children: [
          const PageTitle('我的'),
          const SizedBox(height: 25),
          GroupCard(
            title: '连接',
            children: [
              _SettingsRow(
                icon: CupertinoIcons.cloud,
                color: blue,
                title: '服务器设置',
                trailing: settings.server.isEmpty ? '未设置' : settings.server,
                onTap: () async {
                  final server = await editText(
                    context,
                    title: '服务器设置',
                    label: '服务器地址',
                    initial: settings.server,
                    hint: 'desk.example.com',
                    required: false,
                  );
                  if (server != null) await settings.setServer(server);
                },
              ),
              _SettingsRow(
                icon: CupertinoIcons.device_desktop,
                color: const Color(0xff5856d6),
                title: '已配对电脑',
                trailing: '${services.devices.computers.length} 台',
                onTap: () => Navigator.push<void>(
                  context,
                  MaterialPageRoute(
                    builder: (_) =>
                        _PairedDevices(devices: services.devices, scan: scan),
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 24),
          GroupCard(
            title: '安全与外观',
            children: [
              _SettingsRow(
                icon: CupertinoIcons.lock_shield,
                color: const Color(0xff299c5b),
                title: 'App 锁',
                subtitle: '面容 ID / 指纹',
                customTrailing: CupertinoSwitch(
                  value: settings.locked,
                  activeTrackColor: blue,
                  onChanged: (value) async {
                    if (await authenticate(
                      value ? '验证身份以开启 App 锁' : '验证身份以关闭 App 锁',
                    )) {
                      await settings.setLocked(value);
                    }
                  },
                ),
              ),
              _SettingsRow(
                icon: CupertinoIcons.moon_stars,
                color: const Color(0xff8156d6),
                title: '主题',
                trailing: _themeLabel(settings.theme),
                onTap: () async {
                  final theme = await appSheet<ThemeMode>(
                    context,
                    (context) => KOptionGroup(
                      title: '主题',
                      children: [
                        for (final theme in ThemeMode.values)
                          KRow(
                            title: _themeLabel(theme),
                            trailing: settings.theme == theme
                                ? Icon(
                                    CupertinoIcons.checkmark,
                                    size: 19,
                                    color: context.action,
                                  )
                                : null,
                            onTap: () => Navigator.pop(context, theme),
                          ),
                      ],
                    ),
                    grouped: true,
                  );
                  if (theme != null) await settings.setTheme(theme);
                },
              ),
            ],
          ),
          const SizedBox(height: 24),
          GroupCard(
            children: [
              _SettingsRow(
                icon: CupertinoIcons.info,
                color: const Color(0xff777781),
                title: '关于',
                onTap: () => kDialog<void>(
                  context,
                  builder: (context) => KDialog(
                    title: 'kkdesk',
                    leading: const BrandMark(size: 54),
                    content: const Text('版本 1.0.0'),
                    actions: [
                      KButton(label: '完成', onTap: () => Navigator.pop(context)),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  String _themeLabel(ThemeMode theme) => switch (theme) {
    ThemeMode.system => '跟随系统',
    ThemeMode.light => '浅色',
    ThemeMode.dark => '深色',
  };
}

class _SettingsRow extends StatelessWidget {
  const _SettingsRow({
    required this.icon,
    required this.color,
    required this.title,
    this.subtitle,
    this.trailing,
    this.customTrailing,
    this.onTap,
  });
  final IconData icon;
  final Color color;
  final String title;
  final String? subtitle;
  final String? trailing;
  final Widget? customTrailing;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) => KRow(
    onTap: onTap,
    leading: Container(
      width: 34,
      height: 34,
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(9),
      ),
      alignment: Alignment.center,
      child: Icon(icon, color: Colors.white, size: 20),
    ),
    title: title,
    subtitle: subtitle,
    chevron: onTap != null,
    trailing:
        customTrailing ??
        (trailing == null
            ? null
            : ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 115),
                child: Text(
                  trailing!,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: context.muted, fontSize: 13),
                ),
              )),
  );
}

enum _ComputerAction { rename, unpair }

class _PairedDevices extends StatefulWidget {
  const _PairedDevices({required this.devices, required this.scan});
  final DeviceRepository devices;
  final VoidCallback scan;

  @override
  State<_PairedDevices> createState() => _PairedDevicesState();
}

class _PairedDevicesState extends State<_PairedDevices> {
  final _removing = <String>{};

  Future<void> _showActions(Computer computer) async {
    final action = await appSheet<_ComputerAction>(
      context,
      (context) => KOptionGroup(
        title: computer.name,
        children: [
          KButton(
            label: '更改名称',
            variant: KButtonVariant.text,
            onTap: () => Navigator.pop(context, _ComputerAction.rename),
          ),
          KButton(
            label: '解除配对',
            variant: KButtonVariant.danger,
            fill: Colors.transparent,
            onTap: () => Navigator.pop(context, _ComputerAction.unpair),
          ),
        ],
      ),
      grouped: true,
    );
    if (!mounted) return;
    switch (action) {
      case _ComputerAction.rename:
        await renameComputer(context, widget.devices, computer);
      case _ComputerAction.unpair:
        await _remove(computer);
      case null:
        break;
    }
  }

  Future<void> _remove(Computer computer) async {
    final confirmed = await kConfirm(
      context,
      title: '解除配对？',
      message: '解除后这台手机将不能再连接「${computer.name}」。电脑在线时会同时从电脑上移除。',
      confirm: '解除配对',
      danger: true,
    );
    if (confirmed != true || !mounted || _removing.contains(computer.id)) {
      return;
    }
    setState(() => _removing.add(computer.id));
    try {
      final result = await widget.devices.remove(computer.id);
      if (mounted && result == UnpairResult.localOnly) {
        await kDialog<void>(
          context,
          builder: (context) => KDialog(
            title: '已从手机移除',
            content: const Text('电脑当前不在线，它仍会显示这台手机，可在电脑端「已配对手机」中手动移除。'),
            actions: [
              KButton(label: '完成', onTap: () => Navigator.pop(context)),
            ],
          ),
        );
      }
    } on Exception {
      if (mounted) {
        KNotice.show(context, '无法清除手机上的配对凭据，请重试', kind: KNoticeKind.error);
      }
    } finally {
      if (mounted) setState(() => _removing.remove(computer.id));
    }
  }

  Widget _row(Computer computer) {
    final busy = _removing.contains(computer.id);
    final subtitle = [
      if (computer.online) ...['在线', computer.path.label] else '离线',
      if (computer.lastSeen == '已解除配对')
        '已解除配对'
      else if (computer.confirmOnHost)
        '每次需电脑确认'
      else
        '电脑端已设为免确认',
    ].join(' · ');
    return Slidable(
      key: ValueKey(computer.id),
      groupTag: 'paired-computers',
      enabled: !busy,
      endActionPane: ActionPane(
        motion: const DrawerMotion(),
        extentRatio: .46,
        children: [
          DeviceSwipeAction(
            label: '改名',
            icon: CupertinoIcons.pencil,
            color: const Color(0xff454550),
            onTap: () => renameComputer(context, widget.devices, computer),
          ),
          DeviceSwipeAction(
            label: '解除配对',
            icon: CupertinoIcons.trash,
            color: red,
            onTap: () => _remove(computer),
          ),
        ],
      ),
      child: DeviceRow(
        computer: computer,
        subtitle: subtitle,
        onTap: busy ? null : () => _showActions(computer),
        trailing: SizedBox(
          width: 44,
          height: 35,
          child: Center(
            child: busy
                ? const KLoader(radius: 9)
                : Icon(
                    CupertinoIcons.chevron_right,
                    size: 16,
                    color: context.muted,
                  ),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: SafeArea(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const KPageHeader('已配对电脑'),
          Expanded(
            child: ListenableBuilder(
              listenable: widget.devices,
              builder: (context, _) {
                final computers = widget.devices.computers;
                if (computers.isEmpty) {
                  return Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          '还没有配对的电脑',
                          style: TextStyle(color: context.muted),
                        ),
                        const SizedBox(height: 16),
                        KButton(label: '扫码配对', onTap: widget.scan),
                      ],
                    ),
                  );
                }
                return SlidableAutoCloseBehavior(
                  child: ListView(
                    padding: const EdgeInsets.all(20),
                    children: [
                      GroupCard(
                        children: [
                          for (final computer in computers) _row(computer),
                        ],
                      ),
                    ],
                  ),
                );
              },
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 12, 24, 16),
            child: Center(
              child: Text(
                '免确认需在电脑端「设置 → 安全 → 已配对手机」中开启',
                style: TextStyle(color: context.muted, fontSize: 13),
                textAlign: TextAlign.center,
              ),
            ),
          ),
        ],
      ),
    ),
  );
}
