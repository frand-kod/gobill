#!/bin/sh
# Pemasang NuxBill untuk Armbian/Debian/Fedora dengan systemd. Aman dijalankan ulang.
#   sudo sh install.sh /path/ke/nuxbill-linux-arm64
#   sudo sh install.sh https://example.com/rilis/nuxbill-linux-{arch}   ({arch} -> arm64|armv7|amd64)
#   sudo sh install.sh --uninstall   (hapus service dan binary; data dan config dibiarkan)
set -eu

BIN=/usr/local/bin/nuxbill
CONF_DIR=/etc/nuxbill
CONF=$CONF_DIR/config.env
STATE_DIR=/var/lib/nuxbill
UNIT=/etc/systemd/system/nuxbill.service

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

[ "$(id -u)" -eq 0 ] || die "jalankan sebagai root (sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd tidak ditemukan"

if [ "${1:-}" = "--uninstall" ]; then
	systemctl disable --now nuxbill 2>/dev/null || true
	rm -f "$UNIT" "$BIN"
	systemctl daemon-reload
	log "service dan binary dihapus. Data di $STATE_DIR dan config di $CONF_DIR tetap ada."
	exit 0
fi

SRC=${1:-}
[ -n "$SRC" ] || die "pemakaian: install.sh [--uninstall] <path-binary | URL>"

case "$(uname -m)" in
	aarch64|arm64) ARCH=arm64 ;;
	armv7l) ARCH=armv7 ;;
	x86_64) ARCH=amd64 ;;
	*) die "arsitektur tidak didukung: $(uname -m)" ;;
esac

TMP=$(mktemp) || die "mktemp gagal"
trap 'rm -f "$TMP"' EXIT
case "$SRC" in
	http://*|https://*)
		URL=$(printf '%s' "$SRC" | sed "s/{arch}/$ARCH/g")
		log "mengunduh $URL"
		if command -v curl >/dev/null 2>&1; then
			curl -fsSL -o "$TMP" "$URL" || die "unduhan gagal"
		else
			wget -qO "$TMP" "$URL" || die "unduhan gagal"
		fi
		;;
	*)
		[ -f "$SRC" ] || die "file tidak ditemukan: $SRC"
		cp "$SRC" "$TMP"
		;;
esac

# Hentikan dulu agar binary lama tidak terkunci saat diganti.
if systemctl is-active --quiet nuxbill; then
	systemctl stop nuxbill
fi
log "memasang binary ke $BIN ($ARCH)"
install -m 0755 "$TMP" "$BIN"

if ! id -u nuxbill >/dev/null 2>&1; then
	log "membuat user sistem nuxbill"
	useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin nuxbill
fi
install -d -m 0750 -o nuxbill -g nuxbill "$STATE_DIR"
install -d -m 0755 "$CONF_DIR"

if [ ! -f "$CONF" ]; then
	log "membuat $CONF dengan NUXBILL_SECRET_KEY baru"
	(
		umask 077
		KEY=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
		cat > "$CONF" <<EOF
NUXBILL_DB=$STATE_DIR/nuxbill.db
NUXBILL_HTTP=:8080
# NUXBILL_RADIUS=off mematikan listener UDP built-in (misal jika FreeRADIUS memakai 1812)
NUXBILL_RADIUS=:1812
NUXBILL_SECRET_KEY=$KEY
EOF
	)
	chmod 600 "$CONF"
fi

# Jam STB tanpa RTC harus disinkronkan NTP. Aktifkan salah satu layanan yang ada.
for u in chrony chronyd systemd-timesyncd; do
	if systemctl list-unit-files "$u.service" 2>/dev/null | grep -q "^$u.service"; then
		log "mengaktifkan $u"
		systemctl enable --now "$u" || true
		break
	fi
done

log "memasang unit systemd"
cat > "$UNIT" <<'EOF'
[Unit]
Description=NuxBill billing server
Documentation=https://github.com/frand-kod/nuxbill-go
After=time-sync.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nuxbill
Group=nuxbill
EnvironmentFile=/etc/nuxbill/config.env
ExecStart=/usr/local/bin/nuxbill
StateDirectory=nuxbill
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/nuxbill
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nuxbill

log "terpasang: $("$BIN" --version 2>/dev/null || echo tidak-diketahui)"
echo "Jika ini instalasi baru, password admin pertama hanya dicetak sekali saat start:"
echo "  journalctl -u nuxbill | grep \"first admin\""
