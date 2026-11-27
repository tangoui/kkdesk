import 'desktop_meta.dart';

class DesktopStatus {
  const DesktopStatus({
    this.privacyOn = false,
    this.privacyPending = false,
    this.lockAfterDisconnect = false,
    this.notice,
    this.lockScreen = LockScreenState.unknown,
    this.cursor = RemoteCursor.arrow,
  });

  final bool privacyOn;
  final bool privacyPending;
  final bool lockAfterDisconnect;
  final String? notice;
  final LockScreenState lockScreen;
  final RemoteCursor cursor;

  String? get lockReason => switch (lockScreen) {
    LockScreenState.unknown || LockScreenState.unlocked => null,
    LockScreenState.ready => '对方电脑已锁屏',
    LockScreenState.componentUnavailable => '对方电脑未安装锁屏远控组件',
    LockScreenState.platformUnsupported => '对方系统暂不支持锁屏远程操作',
    LockScreenState.signatureRequired => '锁屏远控不可用（需要正式签名的 kkdesk）',
    LockScreenState.wrongInstallLocation =>
      '对方的 kkdesk 未从安装位置运行，请在对方电脑上从安装位置启动 kkdesk',
    LockScreenState.serviceUnavailable => '对方锁屏远控服务未运行或无法连接助手',
    LockScreenState.componentNeedsUpdate => '请在对方电脑上更新锁屏远控组件',
    LockScreenState.componentError => '对方锁屏远控组件出错，详见对方日志',
    LockScreenState.approvalRequired => '请在对方电脑的系统设置中批准锁屏远控组件',
  };

  DesktopStatus apply(DesktopMeta update) => switch (update) {
    PrivacyReply() => _applyPrivacy(update),
    LockScreenUpdate(:final state) => DesktopStatus(
      privacyOn: privacyOn,
      privacyPending: privacyPending,
      lockAfterDisconnect: lockAfterDisconnect,
      notice: notice,
      lockScreen: state,
      cursor: cursor,
    ),
    CursorUpdate(:final shape) => DesktopStatus(
      privacyOn: privacyOn,
      privacyPending: privacyPending,
      lockAfterDisconnect: lockAfterDisconnect,
      notice: notice,
      lockScreen: lockScreen,
      cursor: shape,
    ),
    UnlockReply() => this,
  };

  DesktopStatus _applyPrivacy(PrivacyReply reply) {
    final status = reply.status;
    final screen = reply.kind == PrivacyReplyKind.screen;
    final lockAfter = reply.kind == PrivacyReplyKind.lockAfterDisconnect;
    return DesktopStatus(
      privacyOn: screen ? status == PrivacyStatus.on : privacyOn,
      privacyPending: screen ? status == PrivacyStatus.waiting : privacyPending,
      lockAfterDisconnect: lockAfter
          ? status == PrivacyStatus.on
          : lockAfterDisconnect,
      notice: switch (reply.kind) {
        PrivacyReplyKind.screen => switch (status) {
          PrivacyStatus.waiting => '等待对方同意…',
          PrivacyStatus.denied => '对方拒绝开启隐私屏',
          PrivacyStatus.notAllowed => '对方不允许开启隐私屏',
          PrivacyStatus.unsupported => _detail('对方无法开启隐私屏', reply.detail),
          PrivacyStatus.localExit => '对方关闭了隐私屏',
          _ => null,
        },
        PrivacyReplyKind.lockAfterDisconnect =>
          reply.detail.isEmpty ? null : reply.detail,
        PrivacyReplyKind.lockNow =>
          status == PrivacyStatus.on
              ? '对方已锁屏'
              : _detail('对方电脑锁屏失败', reply.detail),
      },
      lockScreen: lockScreen,
      cursor: cursor,
    );
  }
}

String _detail(String message, String detail) =>
    detail.isEmpty ? message : '$message：$detail';
