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

class _PairedDevices extends StatelessWidget {
  const _PairedDevices({required this.devices});
  final DeviceRepository devices;

  Future<void> _remove(BuildContext context, Computer computer) async {
    final confirmed = await kConfirm(
      context,
      title: '解除配对？',
      message: '将从手机上移除「${computer.name}」。',
      confirm: '解除配对',
      danger: true,
    );
    if (confirmed == true) await devices.remove(computer.id);
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
              listenable: devices,
              builder: (context, _) {
                final computers = devices.computers;
                if (computers.isEmpty) {
                  return Center(
                    child: Text(
                      '暂无已配对电脑',
                      style: TextStyle(color: context.muted),
                    ),
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
                        KRow(
                          leading: OSIcon(item.os, size: 40, dim: !item.online),
                          title: item.name,
                          subtitle: item.confirmOnHost
                              ? '每次需电脑确认'
                              : '电脑端已设为免确认',
                        ),
                        Row(
                          mainAxisAlignment: MainAxisAlignment.end,
                          children: [
                            KButton(
                              onTap: () =>
                                  renameComputer(context, devices, item),
                              label: '更改名称',
                              variant: KButtonVariant.text,
                              compact: true,
                            ),
                            KButton(
                              onTap: () => _remove(context, item),
                              label: '解除配对',
                              variant: KButtonVariant.danger,
                              compact: true,
                            ),
                            const SizedBox(width: 12),
                          ],
                        ),
                      ],
                    );
                  },
                );
              },
            ),
          ),
        ],
      ),
    ),
  );
}
