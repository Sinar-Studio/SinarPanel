#!/usr/bin/env bash
# Updater SinarPanel: ambil versi terbaru dari git lalu jalankan install.sh.
# Setelah install, tersedia sebagai perintah: sudo sinarpanel-update
#   sudo sinarpanel-update            # update jika ada versi baru
#   sudo sinarpanel-update --check    # hanya cek, tidak install
#   sudo sinarpanel-update --force    # build ulang walau sudah terbaru
#   sudo sinarpanel-update --branch=dev --repo=https://github.com/USER/FORK.git
# Branch default: branch yang terakhir dipakai di /opt/sinarpanel/src (atau main).
set -euo pipefail

DEFAULT_REPO=https://github.com/Sinar-Studio/SinarPanel.git
SRC=/opt/sinarpanel/src
STATE=/etc/sinarpanel/installed-commit
LOCK=/run/sinarpanel-update.lock

REPO=""
BRANCH=""
CHECK_ONLY=0
FORCE=0
ASSUME_YES=0

usage() {
  echo "Pemakaian: sudo sinarpanel-update [--check] [--force] [--branch=NAMA] [--repo=URL] [-y]"
  exit 0
}

info() { printf '\033[1;33m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
ask() { # ask "pertanyaan"  -> exit 0 jika ya (default ya)
  local ans
  if [[ $ASSUME_YES == 1 || ! -t 0 ]]; then return 0; fi
  read -rp "$1 [Y/n] " ans
  [[ ${ans:-y} =~ ^[Yy] ]]
}

# Semua logika di dalam main() supaya bash membaca seluruh file dulu —
# install.sh menimpa file ini saat update berjalan.
main() {
  for arg in "$@"; do
    case $arg in
      --repo=*) REPO=${arg#*=} ;;
      --branch=*) BRANCH=${arg#*=} ;;
      --check) CHECK_ONLY=1 ;;
      --force) FORCE=1 ;;
      -y|--yes) ASSUME_YES=1 ;;
      -h|--help) usage ;;
      *) echo "Argumen tidak dikenal: $arg"; usage ;;
    esac
  done

  [[ $EUID -eq 0 ]] || die "jalankan sebagai root: sudo sinarpanel-update"
  if [[ -z $BRANCH ]]; then
    BRANCH=$(git -C "$SRC" symbolic-ref --short -q HEAD 2>/dev/null || true)
    BRANCH=${BRANCH:-main}
  fi
  [[ $BRANCH =~ ^[A-Za-z0-9._/-]+$ && $BRANCH != -* ]] || die "nama branch tidak valid: $BRANCH"
  if [[ -n $REPO && ! $REPO =~ ^(https://|git@|ssh://) ]]; then
    die "URL repo harus https://, ssh:// atau git@"
  fi

  exec 9>"$LOCK"
  flock -n 9 || die "update lain sedang berjalan"

  if ! command -v git >/dev/null; then
    info "Install git"
    DEBIAN_FRONTEND=noninteractive apt-get install -y -q git
  fi

  # ---------- Ambil source ----------
  if [[ -d $SRC/.git ]]; then
    local cur_repo
    cur_repo=$(git -C "$SRC" remote get-url origin)
    if [[ -n $REPO && $REPO != "$cur_repo" ]]; then
      info "Ganti repo: $cur_repo → $REPO"
      git -C "$SRC" remote set-url origin "$REPO"
    fi
    info "Cek update dari $(git -C "$SRC" remote get-url origin) ($BRANCH)"
    git -C "$SRC" fetch -q origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH" ||
      die "gagal fetch branch $BRANCH"
  else
    REPO=${REPO:-$DEFAULT_REPO}
    info "Clone $REPO ($BRANCH) ke $SRC"
    rm -rf "$SRC"
    install -d -m 0755 -o root -g root "$(dirname "$SRC")"
    git clone -q --branch "$BRANCH" "$REPO" "$SRC" || die "gagal clone $REPO"
  fi

  local new old=""
  new=$(git -C "$SRC" rev-parse "refs/remotes/origin/$BRANCH")
  [[ -f $STATE ]] && old=$(tr -dc '0-9a-f' <"$STATE")

  if [[ $new == "$old" && $FORCE == 0 ]]; then
    echo "SinarPanel sudah versi terbaru (${new:0:7})."
    exit 0
  fi
  if [[ -n $old ]] && git -C "$SRC" cat-file -e "$old^{commit}" 2>/dev/null; then
    echo "Perubahan ${old:0:7} → ${new:0:7}:"
    git -C "$SRC" --no-pager log --oneline --no-decorate "$old..$new" | sed 's/^/  /'
  else
    echo "Versi terbaru: $(git -C "$SRC" --no-pager log -1 --format='%h %s' "$new")"
  fi
  if (( CHECK_ONLY )); then
    exit 0
  fi
  ask "Install versi ini sekarang?" || exit 0

  git -C "$SRC" checkout -q -f -B "$BRANCH" "$new"
  git -C "$SRC" clean -q -fdx

  # ---------- Build & install ----------
  # --no-certbot: update tidak menambah paket yang dulu tidak dipilih.
  # install.sh mencatat commit yang terpasang ke $STATE.
  info "Menjalankan install.sh"
  bash "$SRC/install.sh" -y --no-certbot
  printf '\033[1;32m✓ SinarPanel diupdate ke %s\033[0m\n' "${new:0:7}"
}

main "$@"; exit
