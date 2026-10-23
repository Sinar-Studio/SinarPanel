#!/usr/bin/env bash
# Hapus SinarPanel. Situs Apache, sertifikat, dan file website TIDAK disentuh.
#   sudo bash uninstall.sh          # config & 2FA di /etc/sinarpanel disimpan
#   sudo bash uninstall.sh --purge  # hapus juga /etc/sinarpanel
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "jalankan sebagai root"; exit 1; }

systemctl disable --now sinarpanel 2>/dev/null || true
rm -f /etc/systemd/system/sinarpanel.service /usr/local/bin/sinarpanel /etc/pam.d/sinarpanel
systemctl daemon-reload

if [[ ${1:-} == --purge ]]; then
  rm -rf /etc/sinarpanel
  echo "SinarPanel dihapus beserta config."
else
  echo "SinarPanel dihapus. Config masih ada di /etc/sinarpanel (pakai --purge untuk menghapus)."
fi
echo "Jika pernah membuka port di UFW: ufw delete allow <PORT>/tcp"
