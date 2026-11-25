#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: sudo bash install-server.sh DOMAIN PUBLIC_IPV4 [--binary PATH]
       sudo bash install-server.sh --ip PUBLIC_IPV4 [--listen-port PORT] [--binary PATH]

Installs kkdesk-server on Debian or Ubuntu with systemd.
Point DOMAIN at PUBLIC_IPV4 before running. The installer reuses a
Let's Encrypt certificate for DOMAIN or obtains one on TCP 80.
IP mode generates a persistent server identity and defaults to TCP 8443.
Copy its public key into the kkdesk clients.
EOF
}

fail() { printf 'kkdesk-server: %s\n' "$*" >&2; exit 1; }

if [[ ${1:-} == -h || ${1:-} == --help ]]; then
  usage
  exit
fi
if [[ $# -lt 2 ]]; then
  usage >&2
  exit 2
fi
mode=$1
public_ip=$2
shift 2
binary=
listen_port=443
if [[ $mode == --ip ]]; then
  listen_port=8443
fi
while (( $# )); do
  [[ $# -ge 2 ]] || fail "missing value for $1"
  case $1 in
    --binary) binary=$2; [[ -f $binary && -r $binary ]] || fail 'use --binary with a readable kkdesk-server file' ;;
    --listen-port) listen_port=$2 ;;
    *) fail "unknown option $1" ;;
  esac
  shift 2
done
[[ $listen_port =~ ^[1-9][0-9]{0,4}$ ]] && (( listen_port <= 65535 )) || fail 'invalid HTTPS port'
(( listen_port != 5349 )) || fail 'TCP 5349 is used by TURN TLS'
service_host=$public_ip
if [[ $mode != --ip ]]; then
  label='[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?'
  [[ ${#mode} -le 253 && $mode =~ ^($label\.)+$label$ ]] || fail 'provide a DNS name such as desk.example.com or use --ip'
  service_host=${mode,,}
fi
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
lineage=/etc/letsencrypt/live/$service_host
renewal_hook=/etc/letsencrypt/renewal-hooks/deploy/kkdesk-server
identity_file=/var/lib/kkdesk-server/tls-identity.pem
required_flag=-config
if [[ $mode == --ip ]]; then
  required_flag=-tls-identity
fi
service_url=https://$service_host
if (( listen_port != 443 )); then
  service_url+=:$listen_port
fi

exec 9>/run/lock/kkdesk-server-install.lock
flock -n 9 || fail 'another installation is in progress'
umask 077
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

packages=()
command -v curl >/dev/null || packages+=(curl)
command -v openssl >/dev/null || packages+=(openssl)
[[ -s /etc/ssl/certs/ca-certificates.crt ]] || packages+=(ca-certificates)
if [[ $mode != --ip && ( ! -s $lineage/fullchain.pem || ! -s $lineage/privkey.pem ) ]]; then
  command -v certbot >/dev/null || packages+=(certbot)
fi
if (( ${#packages[@]} )); then
  apt-get update
  apt-get install -y "${packages[@]}"
fi

if [[ -z $binary && -x $installed_binary ]] &&
    "$installed_binary" -help >"$work_dir/help" 2>&1 &&
    grep -Fq -- "$required_flag" "$work_dir/help"; then
  server=$installed_binary
else
  server=$work_dir/server
fi

if [[ -z $binary && $server != "$installed_binary" ]]; then
  # Build the published server revision that introduced -config and /healthz.
  # A local build can be supplied with --binary.
  revision=v0.0.0-20261125062302-3f73b60f4e2a
  if [[ $mode == --ip ]]; then
    revision=main
  fi
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
  grep -Fq -- "$required_flag" "$work_dir/help" || fail "the server binary must support $required_flag"
fi

tls_args="-tls-cert $install_dir/fullchain.pem -tls-key $install_dir/privkey.pem"
if [[ $mode == --ip ]]; then
  if ! curl --help all | grep -Fq -- '--pinnedpubkey'; then
    fail 'curl must support --pinnedpubkey'
  fi
  tls_args="-tls-identity $identity_file"
elif [[ ! -s $lineage/fullchain.pem || ! -s $lineage/privkey.pem ]]; then
  certbot certonly --standalone --non-interactive --agree-tos \
    --register-unsafely-without-email --cert-name "$service_host" -d "$service_host"
fi
[[ $mode == --ip || ( -s $lineage/fullchain.pem && -s $lineage/privkey.pem ) ]] ||
  fail "certificate files are missing under $lineage"

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
ExecStart=$installed_binary -config $config_file -listen :$listen_port $tls_args -turn-public-ip $public_ip -turn-host $service_host -turn-tls-listen :5349
User=kkdesk-server
Group=kkdesk-server
StateDirectory=kkdesk-server
StateDirectoryMode=0700
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
if [ "\${RENEWED_LINEAGE:-}" = "$lineage" ] &&
    [ "\$(cat "$install_dir/tls-lineage" 2>/dev/null || true)" = "$lineage" ]; then
    install -o root -g kkdesk-server -m 0640 "$lineage/fullchain.pem" "$install_dir/fullchain.pem"
    install -o root -g kkdesk-server -m 0640 "$lineage/privkey.pem" "$install_dir/privkey.pem"
    if [ "\${1:-}" = '--copy-only' ]; then exit 0; fi
    systemctl try-restart kkdesk-server.service
fi
EOF
if [[ $mode == --ip ]]; then
  lineage=
fi
printf '%s\n' "$lineage" >"$work_dir/tls-lineage"
install -m 0600 "$work_dir/tls-lineage" "$install_dir/tls-lineage"
if [[ $mode != --ip ]]; then
  install -D -m 0755 "$work_dir/renewal-hook" "$renewal_hook"
  RENEWED_LINEAGE="$lineage" "$renewal_hook" --copy-only
fi

if [[ -n $binary ]]; then
  next_binary=$(mktemp "$installed_binary.XXXXXX")
  install -m 0755 "$server" "$next_binary"
  mv -f "$next_binary" "$installed_binary"
fi
install -m 0644 "$work_dir/kkdesk-server.service" "$unit_file"
systemctl daemon-reload
systemctl enable kkdesk-server.service
systemctl restart kkdesk-server.service

health_args=()
if [[ $mode == --ip ]]; then
  for attempt in {1..8}; do
    [[ -s $identity_file ]] && break
    sleep 1
  done
  [[ -s $identity_file ]] || fail 'server identity was not created. Run sudo journalctl -u kkdesk-server -n 50 --no-pager'
  openssl pkey -in "$identity_file" -pubout -out "$work_dir/public.pem"
  health_args=(--insecure --pinnedpubkey "$work_dir/public.pem")
fi
if ! health=$(curl -fsS "${health_args[@]}" --retry 8 --retry-connrefused --retry-delay 1 --max-time 3 \
  --noproxy '*' --connect-to "$service_host:$listen_port:127.0.0.1:$listen_port" "$service_url/healthz") || [[ $health != ok ]]; then
  fail 'health check failed. Run sudo journalctl -u kkdesk-server -n 50 --no-pager'
fi
printf '\n服务地址  %s\n' "$service_url"
if [[ $mode == --ip ]]; then
  # An Ed25519 SubjectPublicKeyInfo ends with the 32-byte public key.
  public_key=$(openssl pkey -pubin -in "$work_dir/public.pem" -outform DER | tail -c 32 | openssl base64 -A)
  printf '服务器公钥  %s\n' "$public_key"
fi
printf "读取客户端服务密钥  sudo sed -n 's/^KKDESK_SERVER_TOKEN=//p' %s\n" "$env_file"
