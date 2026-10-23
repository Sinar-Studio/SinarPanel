#!/usr/bin/env bash
# Installer SinarPanel untuk Debian / Ubuntu.
#   sudo bash install.sh                 # interaktif
#   sudo bash install.sh --port=9443 -y  # non-interaktif
# Jalankan ulang untuk update (config yang ada tidak ditimpa).
set -euo pipefail

PORT=8443
INSTALL_CERTBOT=1
ASSUME_YES=0
BIN=/usr/local/bin/sinarpanel
CONF_DIR=/etc/sinarpanel
UNIT=/etc/systemd/system/sinarpanel.service
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Pemakaian: sudo bash install.sh [--port=8443] [--no-certbot] [-y]"
  exit 0
}
for arg in "$@"; do
  case $arg in
    --port=*) PORT=${arg#*=} ;;
    --no-certbot) INSTALL_CERTBOT=0 ;;
    -y|--yes) ASSUME_YES=1 ;;
    -h|--help) usage ;;
    *) echo "Argumen tidak dikenal: $arg"; usage ;;
  esac
done

info() { printf '\033[1;33m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;35m[!]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
ask() { # ask "pertanyaan" y|n  -> exit 0 jika ya
  local def=$2 ans hint="y/N"
  [[ $def == y ]] && hint="Y/n"
  if [[ $ASSUME_YES == 1 || ! -t 0 ]]; then [[ $def == y ]]; return; fi
  read -rp "$1 [$hint] " ans
  ans=${ans:-$def}
  [[ $ans =~ ^[Yy] ]]
}

[[ $EUID -eq 0 ]] || die "jalankan sebagai root: sudo bash install.sh"
command -v apt-get >/dev/null || die "installer ini hanya untuk Debian/Ubuntu (apt)"
command -v systemctl >/dev/null || die "butuh systemd"
[[ $PORT =~ ^[0-9]+$ ]] && (( PORT > 0 && PORT < 65536 )) || die "port tidak valid: $PORT"
[[ -f $SRC_DIR/go.mod && -d $SRC_DIR/web ]] || die "jalankan dari folder source SinarPanel"

case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  armv7l|armv6l) GOARCH=armv6l ;;
  *) die "arsitektur $(uname -m) tidak didukung" ;;
esac

if ss -ltnH "sport = :$PORT" 2>/dev/null | grep -q . && ! systemctl is-active --quiet sinarpanel; then
  die "port $PORT sudah dipakai program lain — pakai --port=NOMOR"
fi

# ---------- Paket ----------
info "Install paket yang dibutuhkan"
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
pkgs=(ca-certificates curl gcc libc6-dev libpam0g-dev)
command -v apache2ctl >/dev/null || pkgs+=(apache2)
if (( INSTALL_CERTBOT )) && ! command -v certbot >/dev/null; then
  pkgs+=(certbot python3-certbot-apache)
fi
apt-get install -y -q "${pkgs[@]}"

# ---------- Build ----------
BUILD_TMP=$(mktemp -d)
trap 'rm -rf "$BUILD_TMP"' EXIT
GO=""
if command -v go >/dev/null; then
  minor=$(go env GOVERSION 2>/dev/null | sed -nE 's/^go1\.([0-9]+).*/\1/p')
  if [[ $minor =~ ^[0-9]+$ ]] && (( minor >= 22 )); then GO=$(command -v go); fi
fi
if [[ -z $GO ]]; then
  info "Download Go toolchain (sementara, dihapus setelah build)"
  GOVER=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)
  [[ $GOVER =~ ^go1\.[0-9]+(\.[0-9]+)?$ ]] || die "gagal mengecek versi Go terbaru"
  TGZ="$GOVER.linux-$GOARCH.tar.gz"
  curl -fsSLo "$BUILD_TMP/$TGZ" "https://dl.google.com/go/$TGZ"
  SUM=$(curl -fsSL "https://dl.google.com/go/$TGZ.sha256")
  [[ $SUM =~ ^[0-9a-f]{64}$ ]] || die "gagal mengambil checksum Go"
  echo "$SUM  $BUILD_TMP/$TGZ" | sha256sum -c --quiet - || die "checksum Go tidak cocok!"
  tar -C "$BUILD_TMP" -xzf "$BUILD_TMP/$TGZ"
  GO="$BUILD_TMP/go/bin/go"
fi

info "Build SinarPanel ($("$GO" env GOVERSION))"
(
  cd "$SRC_DIR"
  # GOPROXY=off: tidak ada dependency pihak ketiga yang diunduh.
  CGO_ENABLED=1 GOTOOLCHAIN=local GOPROXY=off GOFLAGS= \
  GOCACHE="$BUILD_TMP/cache" GOPATH="$BUILD_TMP/gopath" \
    "$GO" build -trimpath -buildvcs=false -ldflags "-s -w" -o "$BUILD_TMP/sinarpanel" .
)
"$BUILD_TMP/sinarpanel" version >/dev/null || die "binary hasil build tidak jalan"
install -m 0755 -o root -g root "$BUILD_TMP/sinarpanel" "$BIN"

# ---------- Config ----------
install -d -m 0700 -o root -g root "$CONF_DIR"
if [[ ! -f $CONF_DIR/config.json ]]; then
  info "Membuat config $CONF_DIR/config.json"
  admins=$(getent group sudo wheel admin 2>/dev/null | cut -d: -f4 | tr ',' '\n' | sed '/^$/d' | sort -u | tr '\n' ' ' || true)
  ALLOW_ROOT=false
  if [[ -z ${admins// /} ]]; then
    warn "Belum ada user non-root di grup sudo."
    warn "Disarankan: adduser namakamu && usermod -aG sudo namakamu"
    if ask "Izinkan login panel sebagai root?" y; then ALLOW_ROOT=true; fi
  else
    echo "    User yang bisa login (grup sudo/wheel/admin): $admins"
    if ask "Izinkan juga login sebagai root? (tidak disarankan)" n; then ALLOW_ROOT=true; fi
  fi
  EMAIL=""
  if [[ $ASSUME_YES == 0 && -t 0 ]]; then
    read -rp "Email untuk Let's Encrypt (opsional, Enter untuk lewati): " EMAIL
    if [[ -n $EMAIL && ! $EMAIL =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,63}$ ]]; then
      warn "Email tidak valid, dikosongkan"; EMAIL=""
    fi
  fi
  umask 077
  cat > "$CONF_DIR/config.json" <<EOF
{
  "listen": ":$PORT",
  "allowed_groups": ["sudo", "wheel", "admin"],
  "allowed_users": [],
  "allow_root": $ALLOW_ROOT,
  "allowed_ips": [],
  "require_2fa": false,
  "session_idle_minutes": 30,
  "session_max_hours": 12,
  "bind_session_ip": true,
  "webroot_base": "/var/www",
  "certbot_email": "$EMAIL",
  "max_upload_mb": 1024,
  "max_edit_mb": 5,
  "tls_cert": "$CONF_DIR/tls/cert.pem",
  "tls_key": "$CONF_DIR/tls/key.pem",
  "data_dir": "$CONF_DIR",
  "pam_service": "sinarpanel",
  "apache_sites_available": "/etc/apache2/sites-available",
  "apache_sites_enabled": "/etc/apache2/sites-enabled",
  "apache_service": "apache2",
  "letsencrypt_dir": "/etc/letsencrypt"
}
EOF
  umask 022
else
  info "Config lama dipertahankan"
fi
chown root:root "$CONF_DIR/config.json"
chmod 600 "$CONF_DIR/config.json"
PORT=$(sed -nE 's/.*"listen": *"[^"]*:([0-9]+)".*/\1/p' "$CONF_DIR/config.json")

# ---------- PAM ----------
if [[ ! -f /etc/pam.d/sinarpanel ]]; then
  cat > /etc/pam.d/sinarpanel <<'EOF'
# PAM untuk SinarPanel: sama seperti login password user Linux biasa.
@include common-auth
@include common-account
EOF
  chmod 644 /etc/pam.d/sinarpanel
fi

# ---------- systemd ----------
info "Pasang service systemd"
cat > "$UNIT" <<EOF
[Unit]
Description=SinarPanel - panel VPS ringan
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN serve -config $CONF_DIR/config.json
Restart=on-failure
RestartSec=3
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
UMask=0022
LimitNOFILE=8192
# Hardening. Panel butuh root (Apache/certbot), tapi file manager selalu
# turun hak ke user yang login.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictRealtime=yes
RestrictNamespaces=yes
LockPersonality=yes
SystemCallArchitectures=native
KeyringMode=private

[Install]
WantedBy=multi-user.target
EOF

FP=$("$BIN" cert-fingerprint -config "$CONF_DIR/config.json")
systemctl daemon-reload
systemctl enable sinarpanel >/dev/null 2>&1
systemctl restart sinarpanel
sleep 1
systemctl is-active --quiet sinarpanel || { journalctl -u sinarpanel -n 30 --no-pager; die "service gagal start"; }

# ---------- Firewall ----------
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  if ! ufw status | grep -qE "^$PORT/tcp"; then
    if ask "UFW aktif. Buka port $PORT/tcp untuk panel?" y; then ufw allow "$PORT/tcp" >/dev/null; fi
  fi
fi

IP=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
echo
printf '\033[1;32m✓ SinarPanel terpasang!\033[0m\n\n'
echo "  Buka        : https://${IP:-IP-SERVER}:$PORT"
echo "  Login       : user & password Linux (anggota grup sudo)"
echo "  Fingerprint : $FP"
echo
echo "  Browser akan memperingatkan sertifikat self-signed. Cocokkan fingerprint di atas"
echo "  dengan yang ditampilkan browser sebelum lanjut (klik ikon gembok → sertifikat)."
echo
echo "  Config : $CONF_DIR/config.json   (setelah edit: systemctl restart sinarpanel)"
echo "  Log    : journalctl -u sinarpanel -f"
echo
echo "  Rekomendasi: aktifkan 2FA di menu Keamanan, lalu batasi akses port $PORT"
echo "  ke IP kamu saja (allowed_ips di config, atau firewall)."
