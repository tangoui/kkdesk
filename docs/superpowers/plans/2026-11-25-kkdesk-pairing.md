# kkdesk 扫码配对实施计划

设计以 2026-11-25 手机端设计中已选定的方案 C 为准。

- 持久化电脑 Ed25519 私钥和配对名单。电脑 ID 由其公钥确定，KD 设备码由 ID 导出。二维码使用 MIT 许可的 go-qrcode。
- 配对、状态和会话认证共用签名消息。局域网复用现有 TCP 端口。跨网复用认证过的 WebSocket 入口，服务器按电脑 ID 转发并发放现有 TURN 凭据。手机比对二维码里的电脑指纹，IP 服务器复用 Go 固定公钥逻辑并在 Dart 实现同样的信任规则。签名还绑定 WebRTC SDP，覆盖中间人替换候选连接的路径。
- 桌面新增二维码、允许配对弹窗、名单管理和被控手机名。手机使用 cryptography、flutter_secure_storage、已有 mobile_scanner 和 local_auth。密钥采用 32 字节种子，协议用 base64url 无填充编码公钥、随机数和签名。
- 逐步验证 Go 持久化、配对规则、跨网和局域网端到端，输出可复现的 Go 金样本。Dart 校验 URI、签名、存储、状态和证书，再跑全量检查与后台真机验收。

二维码仅用于当面配对。配置跨网服务时，二维码含共享服务令牌，任何拿到二维码的人均能读取该令牌。

## 消息格式

- 二维码 `kkdesk://pair?v=1&id=…&c=…&fp=…&s=…&t=…&lan=…&pk=…`。LAN 地址以逗号连接。服务器参数随配置出现，`pk` 用标准 Base64 编码服务器 Ed25519 公钥。电脑 ID 是带域分隔的公钥 SHA-256 前 16 字节，`fp` 是完整公钥 SHA-256 的小写十六进制值。兼容的跨网设备码取 ID 文本 SHA-256 的前 12 个 Base32 字符并加 `KD-`。
- 局域网电脑先发原有 HMAC `nonce` 帧。配对手机改发 `{"mobile":{"type":"hello","purpose":"pair","publicKey":"…","nonce":"…"}}`。手机验证签名的 `challenge` 后发带 `code`（以二维码一次性码为密钥，对同一签名输入的 `type=pair-code` 帧做 HMAC-SHA256，再编码为无填充 Base64url）、`name`、`model` 的签名 `request`。电脑消耗一次性码并返回 `result`，`status=pending` 带四位核对码与 Unix 毫秒倒计时，终态为 `ok`、`rejected`、`expired`、`timed_out` 或错误状态。核对码取 SHA-256(配对码 UTF-8 ‖ 电脑公钥 ‖ 手机公钥) 的前四字节，按大端无符号整数取模 10000 后补零。
- 已配对手机使用 `purpose=connect` 获取签名挑战及 `bypass`。通过当前生物识别策略后发送签名 `offer`（`mode` 和原始 SDP），电脑发送签名 `answer`（原始 SDP）。`purpose=status` 和 `purpose=unpair` 使用签名 `request` 与 `result`，撤销返回 `status=unpaired`。
- 签名输入以 `kkdesk/mobile/v1\0` 开头，然后为用途、手机随机数、电脑随机数、手机公钥、电脑公钥和消息字段。消息字段顺序为 `type purpose publicKey nonce code name model os bypass status deadline mode sdp`。每一项使用四字节大端长度加 UTF-8，缺省字符串为空，布尔值为 `false`，时间戳为 `0`。公钥、随机数和签名用无填充 Base64url。
- 跨网复用 `/v1/signal` 的 Bearer 鉴权。电脑以 `mobile_register` 登记固定 ID，签名回应 `mobile_challenge`。手机用 `mobile` 或 `mobile_connect` 进入转发，服务器用会话 ID 转送 `mobile_begin`、`mobile_data`、`mobile_end`，并为 WebRTC 连接发放 ICE/TURN 信息。`mobile_status` 查询固定 ID 的注册状态，手机列表通过签名状态请求同时验证配对关系。
- 普通域名服务器使用系统 CA 校验。IP 公钥方式使用支持 Ed25519 的 TLS 1.3 实现（tlslite 1.0.2），比对叶证书公钥并验证 TLS 握手签名，然后发送服务令牌。跨网转发和媒体中继沿用现有服务器端口。

手机的 Face ID 或指纹检查由官方 App 在发出免确认连接签名前执行。电脑端认证手机 Ed25519 签名和本机名单。IP 公钥校验用于信令 WSS，手机的 TURN 中继验收使用 UDP。
