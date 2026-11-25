# 部署跨网服务

`kkdesk-server` 是 kkdesk 的自托管跨网服务，一个程序里包含两部分：

- **信令**（HTTPS + WebSocket）：被控端用设备码上线，控制端用设备码找到它并交换 WebRTC 连接信息。
- **TURN 中继**（UDP，可选 TLS）：两台电脑无法直接打通时，加密后的画面和操作经它转发。

客户端会自动选择直连或中继。

## 服务器要求

- 一台有**公网 IPv4** 的 Linux 服务器；
- 一个指向它的域名和对应的 **TLS 证书**（例如 Let's Encrypt）；
- 放行下面的端口：

| 端口 | 协议 | 用途 |
|---|---|---|
| 443（由 `-listen` 指定） | TCP | 信令（HTTPS） |
| 3478 | UDP | TURN |
| 49160–49259 | UDP | TURN 中继分配的端口范围 |
| 5349（可选，由 `-turn-tls-listen` 指定） | TCP | TURN over TLS，适合只放行 TCP 的网络 |

服务器在 NAT 后面时，要把这些端口映射进来，并且路由器要支持对公布的公网地址做回环转发。

## 一键安装

支持运行 systemd 的 **Debian／Ubuntu**，架构为 x86_64 或 arm64。先把域名的 A 记录指向实际公网 IPv4。安装脚本会复用已有的 Let's Encrypt 证书，或通过 Certbot 申请证书。申请和续期时，公网需要能访问服务器的 TCP 80，且该端口可由 Certbot 使用。一键安装也会启用 TCP 5349 上的 TURN TLS。

在服务器上单独下载并运行安装脚本，将示例域名和 IP 换成实际值。

```sh
curl -fsSL -o install-server.sh https://raw.githubusercontent.com/tangoui/kkdesk/main/scripts/install-server.sh &&
  sudo bash install-server.sh desk.example.com 203.0.113.10
```

脚本会构建并启动服务，创建 `/etc/kkdesk-server/server.env` 和 `/etc/kkdesk-server/server.json`，并设置开机启动。重复执行会沿用这两个文件。手头已有支持 `-config` 的当前架构二进制时，可以在命令末尾加 `--binary ./kkdesk-server`。

安装成功后，在两台电脑的「设置 → 网络」填写相同的配置。**跨网服务**填安装时使用的域名，例如 `https://desk.example.com`。**服务密钥**填下面第二条命令输出的值。如果忘记了域名，第一条命令可以从一键安装生成的 systemd 服务中查到 HTTPS 地址。

```sh
systemctl show -p ExecStart --value kkdesk-server.service |
  sed -nE 's/.*-turn-host ([^[:space:];]+).*/https:\/\/\1/p'
sudo sed -n 's/^KKDESK_SERVER_TOKEN=//p' /etc/kkdesk-server/server.env
```

填写后点「保存跨网配置」。已经以 root 登录时可以省略 `sudo`。服务端 JSON 配置在 `/etc/kkdesk-server/server.json`，TURN 密钥留在服务器上。

### 下载缓慢或连接被重置

如果下载脚本时出现 `curl: (35) ... Connection reset by peer`，可以通过 jsDelivr 下载同一个脚本。链接固定到发布安装脚本的 Git 提交。把示例域名和 IP 换成实际值。

```sh
curl -fsSL --connect-timeout 5 --max-time 20 -o install-server.sh \
  https://cdn.jsdelivr.net/gh/tangoui/kkdesk@1acc6dfb8d5f63bbe432fe7294f5e5704b62a151/scripts/install-server.sh &&
  sudo bash install-server.sh desk.example.com 203.0.113.10
```

如果是安装脚本下载 Go 或 Go 模块时太慢，可以在网络较快的电脑上准备好 Linux 二进制和安装脚本，再通过 SFTP 或云控制台上传到服务器。服务器上运行 `uname -m` 确认架构，`x86_64` 对应 `amd64`，`aarch64` 对应 `arm64`。

有正式发布包时，从 GitHub Releases 下载对应架构的 `kkdesk-server-<版本>-linux-<架构>.tar.gz` 并解压出 `kkdesk-server`。也可以按下面的「构建」章节生成二进制，ARM64 服务器将 `GOARCH` 改为 `arm64`。[GitHub Releases](https://github.com/tangoui/kkdesk/releases)

将 `install-server.sh` 和可执行文件 `kkdesk-server` 放到服务器的同一个目录，在该目录运行下面的命令。已经以 root 登录时可以省略 `sudo`。

```sh
sudo bash install-server.sh desk.example.com 203.0.113.10 --binary ./kkdesk-server
```

`--binary` 会跳过 Go 工具链下载、Go 模块下载和服务端编译。安装所需的系统软件包和证书仍由脚本准备。

## 构建

在仓库根目录：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o kkdesk-server ./cmd/kkdesk-server
```

得到一个静态链接的可执行文件，复制到服务器即可。

## 启动

```sh
export KKDESK_SERVER_TOKEN="$(openssl rand -hex 32)"   # 客户端要填的服务密钥
export KKDESK_TURN_SECRET="$(openssl rand -hex 32)"    # 只留在服务器上

./kkdesk-server -listen :443 \
  -tls-cert /etc/letsencrypt/live/desk.example.com/fullchain.pem \
  -tls-key  /etc/letsencrypt/live/desk.example.com/privkey.pem \
  -turn-public-ip 203.0.113.10 \
  -turn-host desk.example.com -turn-tls-listen :5349
```

把域名和 IP 换成服务器的实际值。**重启服务时请使用相同的两个密钥**，否则所有客户端都要重新填写服务密钥。

## 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-config` | | 从 JSON 文件读取部署参数。 |
| `-listen` | `127.0.0.1:8080` | 信令监听地址。对外服务一般用 `:443`。 |
| `-tls-cert` | | TLS 证书（含证书链）的 PEM 文件。 |
| `-tls-key` | | TLS 私钥的 PEM 文件。 |
| `-turn-listen` | `0.0.0.0:3478` | TURN UDP 监听地址。 |
| `-turn-tls-listen` | | 可选的 TURN over TLS 监听地址，例如 `:5349`。 |
| `-turn-host` | | TURN TLS 使用的公网域名。 |
| `-turn-public-ip` | | 公布给客户端的中继公网 IPv4。 |
| `-relay-bind` | `0.0.0.0` | 中继分配绑定的本机 IPv4。 |
| `-relay-min` | `49160` | 中继 UDP 端口范围的起点。 |
| `-relay-max` | `49259` | 中继 UDP 端口范围的终点。 |
| `-relay-only` | `false` | 强制所有媒体走中继，用来验证中继是否可用。 |
| `-max-signaling-connections` | `2048` | 同时开放的信令连接上限，包含在线设备和正在配对的控制端。 |
| `-turn-credential-ttl` | `8h` | TURN 凭据有效期。缩短有效期会影响长时间中继会话的续期。 |

| 环境变量 | 说明 |
|---|---|
| `KKDESK_SERVER_TOKEN` | 服务密钥。客户端在「设置 › 网络 › 服务密钥」里填它。 |
| `KKDESK_TURN_SECRET` | 签发 TURN 临时凭据的密钥，只留在服务器上。 |

可用 `./kkdesk-server -config server.json` 启动。JSON 键名与命令行参数相同，去掉开头的 `-`。命令行上显式提供的参数优先。两个密钥通过环境变量设置。[完整配置示例](https://github.com/tangoui/kkdesk#服务端配置文件)

## 用 systemd 常驻

```ini
# /etc/systemd/system/kkdesk-server.service
[Unit]
Description=kkdesk cross-network service
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/kkdesk-server.env
ExecStart=/usr/local/bin/kkdesk-server -listen :443 \
  -tls-cert /etc/letsencrypt/live/desk.example.com/fullchain.pem \
  -tls-key /etc/letsencrypt/live/desk.example.com/privkey.pem \
  -turn-public-ip 203.0.113.10 -turn-host desk.example.com -turn-tls-listen :5349
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

`/etc/kkdesk-server.env` 里写两个密钥（权限设为 600）：

```sh
KKDESK_SERVER_TOKEN=...
KKDESK_TURN_SECRET=...
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now kkdesk-server
```

## 客户端配置与验证

访问 `https://desk.example.com/healthz`。返回 `ok` 表示 HTTPS 信令服务正在响应。中继连通性可通过强制中继验证。

1. 两台电脑在「设置 › 网络」填同一个 **跨网服务** 地址（`https://desk.example.com`）和 **服务密钥**，点「保存跨网配置」。
2. 被控端开启「允许被远程控制」，连接页出现 `KD-` 开头的设备码，说明已经在服务上线。
3. 控制端用设备码连接，被控端接受。
4. 想确认中继可用：给服务端加 `-relay-only` 重启，再连一次，控制中心里「中继连接」应该被勾上。验证完记得去掉。

## 当前限制

- 单实例，所有设备共用一个服务密钥。
- 设备上线与配对状态只保存在内存里。服务重启后，被控端每 3 秒自动重连，设备码不变。
