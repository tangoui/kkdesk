#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: sudo bash install-server.sh DOMAIN PUBLIC_IPV4 [--binary PATH]

Installs kkdesk-server on Debian or Ubuntu with systemd.
Point DOMAIN at PUBLIC_IPV4 before running. The installer reuses a
Let's Encrypt certificate for DOMAIN or obtains one on TCP 80.
EOF
}

fail() { printf 'kkdesk-server: %s\n' "$*" >&2; exit 1; }

if [[ ${1:-} == -h || ${1:-} == --help ]]; then
  usage
  exit
fi
if [[ $# -ne 2 && $# -ne 4 ]]; then
  usage >&2
  exit 2
fi
domain=$1
public_ip=$2
binary=
if [[ $# == 4 ]]; then
  [[ $3 == --binary && -f $4 && -r $4 ]] || fail 'use --binary with a readable kkdesk-server file'
  binary=$4
fi

label='[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?'
[[ ${#domain} -le 253 && $domain =~ ^($label\.)+$label$ ]] || fail 'provide a DNS name such as desk.example.com'
domain=${domain,,}
[[ $public_ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || fail 'provide an IPv4 address'
IFS=. read -r -a octets <<< "$public_ip"
for octet in "${octets[@]}"; do
  [[ $octet == 0 || $octet != 0* ]] && (( 10#$octet <= 255 )) || fail 'invalid IPv4 address'
done

[[ $(uname -s) == Linux && -d /run/systemd/system ]] || fail 'run this installer on Linux with systemd running'
(( EUID == 0 )) || fail 'run this installer with sudo'
command -v apt-get >/dev/null || fail 'this installer requires Debian or Ubuntu'
case $(uname -m) in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) fail 'supported architectures are x86_64 and aarch64' ;;
esac

install_dir=/etc/kkdesk-server
env_file=$install_dir/server.env
config_file=$install_dir/server.json
installed_binary=/usr/local/bin/kkdesk-server
unit_file=/etc/systemd/system/kkdesk-server.service
lineage=/etc/letsencrypt/live/$domain
renewal_hook=/etc/letsencrypt/renewal-hooks/deploy/kkdesk-server

exec 9>/run/lock/kkdesk-server-install.lock
flock -n 9 || fail 'another installation is in progress'
umask 077
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

packages=()
command -v curl >/dev/null || packages+=(curl)
command -v openssl >/dev/null || packages+=(openssl)
[[ -s /etc/ssl/certs/ca-certificates.crt ]] || packages+=(ca-certificates)
if [[ ! -s $lineage/fullchain.pem || ! -s $lineage/privkey.pem ]]; then
  command -v certbot >/dev/null || packages+=(certbot)
fi
if (( ${#packages[@]} )); then
  apt-get update
  apt-get install -y "${packages[@]}"
fi

if [[ -z $binary && -x $installed_binary ]] &&
    "$installed_binary" -help >"$work_dir/help" 2>&1 &&
    grep -Fq -- '-config' "$work_dir/help"; then
  server=$installed_binary
else
  server=$work_dir/server
fi

if [[ -z $binary && $server != "$installed_binary" ]]; then
  # Build the published server revision that introduced -config and /healthz.
  # A local build can be supplied with --binary.
  revision=v0.0.0-20261125062302-3f73b60f4e2a
  if [[ $(go version 2>/dev/null || true) =~ go1\.([0-9]+) ]] && (( BASH_REMATCH[1] >= 27 )); then
    go_bin=$(command -v go)
  else
    go_version=1.27.4
    case $arch in
      amd64) checksum=7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840 ;;
      arm64) checksum=3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92 ;;
    esac
    curl -fsSL "https://go.dev/dl/go$go_version.linux-$arch.tar.gz" -o "$work_dir/go.tar.gz"
    printf '%s  %s\n' "$checksum" "$work_dir/go.tar.gz" | sha256sum -c -
    tar -xzf "$work_dir/go.tar.gz" -C "$work_dir"
    go_bin=$work_dir/go/bin/go
  fi
  printf 'Building kkdesk-server for Linux %s\n' "$arch"
  export GOBIN="$work_dir" GOENV=off GOWORK=off GOTOOLCHAIN=local GOOS=linux GOARCH="$arch" CGO_ENABLED=0
  if ! "$go_bin" install -trimpath "github.com/tangoui/kkdesk/cmd/kkdesk-server@$revision"; then
    printf 'Retrying the server build\n' >&2
    "$go_bin" install -trimpath "github.com/tangoui/kkdesk/cmd/kkdesk-server@$revision"
  fi
  binary=$work_dir/kkdesk-server
fi

if [[ -n $binary ]]; then
  install -m 0755 "$binary" "$server"
  "$server" -help >"$work_dir/help" 2>&1 || fail 'unable to run the supplied kkdesk-server on this machine'
  grep -Fq -- '-config' "$work_dir/help" || fail 'the server binary must support -config'
fi

if [[ ! -s $lineage/fullchain.pem || ! -s $lineage/privkey.pem ]]; then
  certbot certonly --standalone --non-interactive --agree-tos \
    --register-unsafely-without-email --cert-name "$domain" -d "$domain"
fi
[[ -s $lineage/fullchain.pem && -s $lineage/privkey.pem ]] || fail "certificate files are missing under $lineage"

getent group kkdesk-server >/dev/null || groupadd --system kkdesk-server
if ! id -u kkdesk-server >/dev/null 2>&1; then
  useradd --system --gid kkdesk-server --home-dir /nonexistent --shell /usr/sbin/nologin kkdesk-server
fi
[[ $(id -gn kkdesk-server) == kkdesk-server ]] || fail 'the kkdesk-server account must use the kkdesk-server group'

install -d -o root -g kkdesk-server -m 0750 "$install_dir"
if [[ ! -e $env_file ]]; then
  printf 'KKDESK_SERVER_TOKEN=%s\nKKDESK_TURN_SECRET=%s\n' \
    "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" >"$work_dir/server.env"
  install -m 0600 "$work_dir/server.env" "$env_file"
fi
if [[ ! -e $config_file ]]; then
  printf '{}\n' >"$work_dir/server.json"
  install -o root -g kkdesk-server -m 0640 "$work_dir/server.json" "$config_file"
fi
chgrp kkdesk-server "$config_file"
chmod 0640 "$config_file"

cat >"$work_dir/kkdesk-server.service" <<EOF
[Unit]
Description=kkdesk cross-network service
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=$env_file
ExecStart=$installed_binary -config $config_file -listen :443 -tls-cert $install_dir/fullchain.pem -tls-key $install_dir/privkey.pem -turn-public-ip $public_ip -turn-host $domain -turn-tls-listen :5349
User=kkdesk-server
Group=kkdesk-server
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=multi-user.target
EOF

cat >"$work_dir/renewal-hook" <<EOF
#!/bin/sh
set -eu
if [ "\${RENEWED_LINEAGE:-}" = "$lineage" ]; then
    install -o root -g kkdesk-server -m 0640 "$lineage/fullchain.pem" "$install_dir/fullchain.pem"
    install -o root -g kkdesk-server -m 0640 "$lineage/privkey.pem" "$install_dir/privkey.pem"
    if [ "\${1:-}" = '--copy-only' ]; then exit 0; fi
    systemctl try-restart kkdesk-server.service
fi
EOF
install -D -m 0755 "$work_dir/renewal-hook" "$renewal_hook"
RENEWED_LINEAGE="$lineage" "$renewal_hook" --copy-only

if [[ -n $binary ]]; then
  next_binary=$(mktemp "$installed_binary.XXXXXX")
  install -m 0755 "$server" "$next_binary"
  mv -f "$next_binary" "$installed_binary"
fi
install -m 0644 "$work_dir/kkdesk-server.service" "$unit_file"
systemctl daemon-reload
systemctl enable kkdesk-server.service
systemctl restart kkdesk-server.service

if ! health=$(curl -fsS --retry 8 --retry-connrefused --retry-delay 1 --max-time 3 \
  --noproxy '*' --resolve "$domain:443:127.0.0.1" "https://$domain/healthz") || [[ $health != ok ]]; then
  fail 'health check failed. Run sudo journalctl -u kkdesk-server -n 50 --no-pager'
fi

printf '\n服务地址  https://%s\n' "$domain"
printf "读取客户端服务密钥  sudo sed -n 's/^KKDESK_SERVER_TOKEN=//p' %s\n" "$env_file"
