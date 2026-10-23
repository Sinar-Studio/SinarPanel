# SinarPanel ☀️

Panel VPS super ringan untuk server **Apache (Debian/Ubuntu)**: tambah subdomain, pasang SSL Let's Encrypt, dan file manager — semua lewat browser, tanpa SSH.

- **Satu binary Go**, tanpa PHP / Node / Python / database. Nol dependency pihak ketiga (hanya standard library Go + PAM sistem).
- RAM ±10–20 MB.

## Fitur

| Menu | Isi |
|---|---|
| **Dashboard** | Info server (OS, uptime, load, RAM, disk), status Apache + tombol configtest / reload / restart, install certbot sekali klik |
| **Situs & SSL** | Tambah subdomain (static/doc root **atau** reverse proxy ke `127.0.0.1:PORT` + WebSocket), langsung pasang SSL + redirect HTTPS, cek DNS, aktif/nonaktif, edit config vhost, hapus |
| **File Manager** | Jelajah, upload (drag & drop + progress), download (file atau folder → zip), editor teks, buat file/folder, rename/pindah, salin, chmod, extract zip/tar.gz, hapus |
| **Keamanan** | 2FA TOTP (Google Authenticator, Authy, 2FAS, Bitwarden…) |

Setiap perubahan Apache otomatis menjalankan `apache2ctl configtest`; kalau gagal, perubahan **di-rollback** sehingga Apache tidak pernah mati karena salah config.

## Install

Di server (Debian 11+/Ubuntu 20.04+), sebagai root:

```bash
# salin folder ini ke server, misalnya:
scp -r SinarPanel user@IP-SERVER:~/
ssh user@IP-SERVER
cd SinarPanel
sudo bash install.sh
```

Opsi: `--port=9443` (default 8443), `--no-certbot`, `-y` (non-interaktif).

Installer akan:
1. Install `gcc`, `libpam0g-dev` (untuk build), `apache2` & `certbot` jika belum ada.
2. Download Go **sementara** (checksum diverifikasi), build binary, lalu menghapus toolchain-nya.
3. Membuat `/etc/sinarpanel/config.json` (mode 600), PAM `/etc/pam.d/sinarpanel`, dan service systemd.
4. Menampilkan URL + **fingerprint sertifikat**.

Buka `https://IP-SERVER:8443`. Browser akan memperingatkan sertifikat self-signed — cocokkan fingerprint SHA-256 yang ditampilkan installer dengan yang ada di browser, lalu lanjutkan.

**Update:** tarik versi baru ke folder yang sama, lalu jalankan `sudo bash install.sh` lagi (config tidak ditimpa).
**Uninstall:** `sudo bash uninstall.sh` (tambahkan `--purge` untuk menghapus config). Situs & sertifikat tidak disentuh.

## Login

Pakai **user & password Linux** (sama seperti SSH password) — diverifikasi lewat PAM.

- Default yang boleh login hanya anggota grup `sudo`, `wheel`, atau `admin`.
- Login `root` **dimatikan** secara default (`allow_root`). Disarankan buat user admin:
  ```bash
  adduser namakamu && usermod -aG sudo namakamu
  ```
- Kalau SSH kamu hanya pakai key dan user belum punya password, set dulu dengan `passwd namakamu`.

## Model keamanan

| Lapisan | Implementasi |
|---|---|
| Transport | HTTPS wajib (TLS ≥ 1.2). Self-signed otomatis atau cert sendiri, auto-reload saat cert diperbarui |
| Autentikasi | PAM (password user Linux) + 2FA TOTP opsional/wajib, anti-replay kode |
| Brute force | 5 gagal / IP → kunci 15 menit; 10 gagal / username → kunci 15 menit; maks 4 PAM paralel |
| Otorisasi | Hanya user di grup/daftar yang diizinkan; shell `nologin`/`false` ditolak; akun expired ditolak |
| Sesi | Token acak 256-bit (disimpan sebagai hash), cookie `__Host-` `Secure` `HttpOnly` `SameSite=Strict`, idle timeout 30 menit, maks 12 jam, terikat IP |
| CSRF | Token per sesi di header `X-CSRF-Token` + cek `Origin` / `Sec-Fetch-Site` |
| **File manager** | Setiap operasi dijalankan oleh proses terpisah yang **turun hak ke UID/GID user yang login** — kernel yang menegakkan permission. User `budi` tidak bisa membaca file yang tidak bisa ia baca lewat SSH |
| Path berbahaya | Proteksi hapus/pindah `/`, `/etc`, `/home`, `/var/www`, `/usr/bin`, `/root/.ssh`, dll. Extract arsip anti zip-slip, symlink/device di arsip diabaikan |
| Injeksi config | Domain, alias, document root, URL proxy divalidasi ketat (whitelist karakter) sebelum ditulis ke vhost. Semua perintah dijalankan langsung (tanpa shell) |
| Browser | CSP ketat (tanpa inline script, tanpa CDN), `X-Frame-Options: DENY`, `nosniff`, `no-referrer`. File user selalu di-download sebagai attachment + CSP sandbox, tidak pernah dirender di origin panel |
| Audit | Semua login & aksi tercatat: `journalctl -u sinarpanel \| grep AUDIT` |
| systemd | `NoNewPrivileges`, `PrivateTmp`, `ProtectKernel*`, `RestrictNamespaces`, dll. |

> Panel berjalan sebagai root (karena harus mengelola Apache & certbot), sama seperti Webmin/Cockpit. Karena itu, yang boleh login dibatasi ke admin (sudoers) saja.

### Rekomendasi hardening

1. Aktifkan 2FA di menu **Keamanan**, lalu set `"require_2fa": true`.
2. Batasi akses port panel hanya dari IP kamu:
   ```json
   "allowed_ips": ["203.0.113.10", "10.8.0.0/24"]
   ```
   atau lewat firewall: `ufw allow from 203.0.113.10 to any port 8443 proto tcp`.
3. Ideal: tutup port panel dari internet dan akses lewat SSH tunnel / VPN (WireGuard/Tailscale):
   ```bash
   ssh -L 8443:127.0.0.1:8443 user@IP-SERVER
   ```
   lalu set `"listen": "127.0.0.1:8443"` dan buka `https://localhost:8443`.

## Konfigurasi

`/etc/sinarpanel/config.json` — setelah edit jalankan `systemctl restart sinarpanel`.

| Key | Default | Keterangan |
|---|---|---|
| `listen` | `:8443` | Alamat:port panel |
| `allowed_groups` | `sudo, wheel, admin` | Grup yang boleh login |
| `allowed_users` | `[]` | User tambahan yang boleh login |
| `allow_root` | `false` | Izinkan login sebagai root |
| `allowed_ips` | `[]` (semua) | Whitelist IP/CIDR akses panel |
| `require_2fa` | `false` | Wajibkan 2FA untuk semua user |
| `session_idle_minutes` | `30` | Auto logout jika tidak aktif |
| `session_max_hours` | `12` | Umur maksimum sesi |
| `bind_session_ip` | `true` | Sesi tidak bisa dipakai dari IP lain |
| `webroot_base` | `/var/www` | Document root situs baru harus di dalam folder ini |
| `certbot_email` | `""` | Email default Let's Encrypt |
| `max_upload_mb` | `1024` | Batas upload per request |
| `max_edit_mb` | `5` | Batas ukuran file di editor |
| `tls_cert` / `tls_key` | `/etc/sinarpanel/tls/*.pem` | Sertifikat panel |

### Pakai domain + SSL valid untuk panel

Buat subdomain (misal `panel.domainku.com`) lewat SinarPanel dan pasang SSL-nya, lalu arahkan config panel ke sertifikat tersebut:

```json
"tls_cert": "/etc/letsencrypt/live/panel.domainku.com/fullchain.pem",
"tls_key":  "/etc/letsencrypt/live/panel.domainku.com/privkey.pem"
```

Buka `https://panel.domainku.com:8443`. Sertifikat di-reload otomatis ketika certbot memperpanjangnya.

## Troubleshooting

| Masalah | Solusi |
|---|---|
| Login selalu gagal | Pastikan user di grup sudo (`groups namakamu`) & punya password. Cek `journalctl -u sinarpanel -n 50` |
| "Terlalu banyak percobaan" | Tunggu 15 menit, atau `systemctl restart sinarpanel` |
| certbot gagal | DNS A record harus mengarah ke IP server & port 80 terbuka (`ufw allow 80,443/tcp`). Pakai tombol "Cek DNS" |
| certbot versi snap | Hapus baris `RestrictNamespaces=yes` di `/etc/systemd/system/sinarpanel.service`, lalu `systemctl daemon-reload && systemctl restart sinarpanel` |
| Lupa HP / 2FA hilang | Via SSH: `sudo rm /etc/sinarpanel/2fa/USERNAME.json` |
| Ganti port | Edit `listen` di config, restart, dan buka port baru di firewall |

## Struktur kode

```
main.go        entry point & subcommand
server.go      HTTP server, routing, security header
auth.go        login PAM, sesi, rate limit, CSRF, 2FA endpoint
pam_cgo.go     binding PAM (cgo)
totp.go        TOTP RFC 6238
sites.go       vhost Apache, certbot, cek DNS
files.go       endpoint file manager → fs-helper
fshelper.go    operasi file (berjalan sebagai user login)
sysinfo.go     info dashboard
tlscert.go     sertifikat self-signed & auto reload
web/           UI (HTML/CSS/JS vanilla, di-embed ke binary)
```

Tes: `go test ./...`
