import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../mobile_services.dart';
import 'common.dart';

class SettingsPage extends StatelessWidget {
  const SettingsPage({
    super.key,
    required this.services,
    required this.authenticate,
  });
  final MobileServices services;
  final Future<bool> Function(String reason) authenticate;

  @override
  Widget build(BuildContext context) {
    final settings = services.settings;
    return ListenableBuilder(
      listenable: Listenable.merge([settings, services.devices]),
      builder: (context, _) => ListView(
        padding: const EdgeInsets.fromLTRB(20, 22, 20, 26),
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
                    builder: (_) => _PairedDevices(devices: services.devices),
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
                    (context) => Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          '主题',
                          style: Theme.of(context).textTheme.titleLarge,
                        ),
                        const SizedBox(height: 10),
                        for (final theme in ThemeMode.values)
                          ListTile(
                            title: Text(_themeLabel(theme)),
                            trailing: settings.theme == theme
                                ? Icon(
                                    CupertinoIcons.checkmark,
                                    color: context.action,
                                  )
                                : null,
                            onTap: () => Navigator.pop(context, theme),
                          ),
                      ],
                    ),
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
                onTap: () => showAboutDialog(
                  context: context,
                  applicationName: 'kkdesk',
                  applicationVersion: '1.0.0',
                  applicationIcon: const Icon(
                    CupertinoIcons.device_desktop,
                    color: blue,
                    size: 36,
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
  Widget build(BuildContext context) => ListTile(
    onTap: onTap,
    minTileHeight: 62,
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
    title: Text(title, style: const TextStyle(fontWeight: FontWeight.w500)),
    subtitle: subtitle == null
        ? null
        : Text(subtitle!, style: TextStyle(color: context.muted, fontSize: 12)),
    trailing:
        customTrailing ??
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (trailing case final trailing?) ...[
              ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 115),
                child: Text(
                  trailing,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: context.muted),
                ),
              ),
              const SizedBox(width: 8),
            ],
            Icon(CupertinoIcons.chevron_right, size: 15, color: context.muted),
          ],
        ),
  );
}

class _PairedDevices extends StatelessWidget {
  const _PairedDevices({required this.devices});
  final DeviceRepository devices;

  Future<void> _remove(BuildContext context, Computer computer) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('解除配对？'),
        content: Text('将从手机上移除「${computer.name}」。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('解除配对', style: TextStyle(color: red)),
          ),
        ],
      ),
    );
    if (confirmed == true) await devices.remove(computer.id);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('已配对电脑')),
    body: SafeArea(
      top: false,
      child: ListenableBuilder(
        listenable: devices,
        builder: (context, _) {
          final computers = devices.computers;
          if (computers.isEmpty) {
            return Center(
              child: Text('暂无已配对电脑', style: TextStyle(color: context.muted)),
            );
          }
          return ListView.separated(
            itemCount: computers.length,
            padding: const EdgeInsets.all(20),
            separatorBuilder: (_, _) => const SizedBox(height: 12),
            itemBuilder: (context, index) {
              final item = computers[index];
              return GroupCard(
                children: [
                  ListTile(
                    leading: OSIcon(item.os, size: 40, dim: !item.online),
                    title: Text(
                      item.name,
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    subtitle: Text(
                      item.confirmOnHost ? '每次需电脑确认' : '电脑端已设为免确认',
                    ),
                  ),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      TextButton(
                        onPressed: () => renameComputer(context, devices, item),
                        child: const Text('更改名称'),
                      ),
                      TextButton(
                        onPressed: () => _remove(context, item),
                        child: const Text('解除配对', style: TextStyle(color: red)),
                      ),
                      const SizedBox(width: 8),
                    ],
                  ),
                ],
              );
            },
          );
        },
      ),
    ),
  );
}
