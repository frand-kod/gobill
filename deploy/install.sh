#!/bin/sh
# Pemasang gobill untuk Armbian/Debian/Fedora dengan systemd. Aman dijalankan ulang.
#   sudo sh install.sh /path/ke/gobill-linux-arm64
#   sudo sh install.sh https://example.com/rilis/gobill-linux-{arch}   ({arch} -> arm64|armv7|amd64)
#   sudo sh install.sh --uninstall   (hapus service dan binary; data dan config dibiarkan)
set -eu

BIN=/usr/local/bin/gobill
CONF_DIR=/etc/gobill
CONF=$CONF_DIR/config.env
STATE_DIR=/var/lib/gobill
UNIT=/etc/systemd/system/gobill.service

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

# Kunci publik rilis (sama dengan internal/update/release.pub, dalam bentuk PEM).
PUBPEM='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEALHIK3Gd13BFfHN2yvRRhOQYlBAOuRZpiqY5Q4ihg7Bw=
-----END PUBLIC KEY-----'

# verify_sums SUMS SIG: SIG berisi tanda tangan ed25519 base64 atas byte mentah SUMS. Gagal = tolak.
verify_sums() {
	command -v openssl >/dev/null 2>&1 || die "openssl tidak ada, tanda tangan tidak bisa diperiksa (pasang openssl, atau GOBILL_INSECURE=1 dengan risiko sendiri)"
	_k=$1.key; _b=$2.bin
	printf '%s\n' "$PUBPEM" > "$_k"
	openssl base64 -d -A -in "$2" -out "$_b" 2>/dev/null || { rm -f "$_k" "$_b"; return 1; }
	# -rawin ada di OpenSSL 3; OpenSSL 1.1.1 memeriksa ed25519 tanpa -rawin.
	{ openssl pkeyutl -verify -pubin -inkey "$_k" -rawin -in "$1" -sigfile "$_b" ||
	  openssl pkeyutl -verify -pubin -inkey "$_k" -in "$1" -sigfile "$_b"; } >/dev/null 2>&1
	_r=$?
	rm -f "$_k" "$_b"
	return $_r
}

[ "$(id -u)" -eq 0 ] || die "jalankan sebagai root (sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd tidak ditemukan"

if [ "${1:-}" = "--uninstall" ]; then
	systemctl disable --now gobill 2>/dev/null || true
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
		fetch() {
			if command -v curl >/dev/null 2>&1; then
				curl -fsSL -o "$2" "$1"
			else
				wget -qO "$2" "$1"
			fi
		}
		log "mengunduh $URL"
		fetch "$URL" "$TMP" || die "unduhan gagal"
		# Rilis GitHub: periksa tanda tangan SHA256SUMS (kunci tertanam di atas), lalu cocokkan hash binary. Binary tidak dijalankan sebelum lolos.
		case "$URL" in
			https://github.com/*/releases/*download/*)
				BASE=${URL%/*}
				SUMS=$TMP.sums
				trap 'rm -f "$TMP" "$SUMS" "$SUMS.sig" "$SUMS.key" "$SUMS.sig.bin"' EXIT
				log "memverifikasi SHA256SUMS"
				fetch "$BASE/SHA256SUMS" "$SUMS" || die "SHA256SUMS tidak bisa diunduh"
				if [ "${GOBILL_INSECURE:-}" = 1 ]; then
					echo "peringatan: tanda tangan tidak diperiksa (GOBILL_INSECURE=1)" >&2
				else
					fetch "$BASE/SHA256SUMS.sig" "$SUMS.sig" || die "SHA256SUMS.sig tidak bisa diunduh"
					verify_sums "$SUMS" "$SUMS.sig" || die "tanda tangan SHA256SUMS tidak valid; unduhan ditolak"
				fi
				WANT=$(awk -v f="${URL##*/}" '$2 == f || $2 == "*" f {print $1; exit}' "$SUMS")
				[ -n "$WANT" ] || die "${URL##*/} tidak ada di SHA256SUMS"
				[ "$(sha256sum "$TMP" | cut -d' ' -f1)" = "$WANT" ] || die "checksum tidak cocok; unduhan ditolak"
				;;
		esac
		;;
	*)
		[ -f "$SRC" ] || die "file tidak ditemukan: $SRC"
		cp "$SRC" "$TMP"
		;;
esac

# Hentikan dulu agar binary lama tidak terkunci saat diganti.
if systemctl is-active --quiet gobill; then
	systemctl stop gobill
fi
log "memasang binary ke $BIN ($ARCH)"
install -m 0755 "$TMP" "$BIN"

if ! id -u gobill >/dev/null 2>&1; then
	log "membuat user sistem gobill"
	useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin gobill
fi
install -d -m 0750 -o gobill -g gobill "$STATE_DIR"
install -d -m 0755 "$CONF_DIR"

if [ ! -f "$CONF" ]; then
	log "membuat $CONF dengan GOBILL_SECRET_KEY baru"
	(
		umask 077
		KEY=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
		# Template berkomentar lengkap (Inggris) dari binary; binary lama tanpa perintah ini memakai versi singkat.
		if "$BIN" config-example > "$CONF.tmp" 2>/dev/null && grep -q '^GOBILL_SECRET_KEY=' "$CONF.tmp"; then
			sed -e "s#^GOBILL_DB=.*#GOBILL_DB=$STATE_DIR/gobill.db#" -e "s#^GOBILL_SECRET_KEY=.*#GOBILL_SECRET_KEY=$KEY#" "$CONF.tmp" > "$CONF"
		else
			cat > "$CONF" <<EOF
GOBILL_DB=$STATE_DIR/gobill.db
GOBILL_HTTP=:8080
# GOBILL_RADIUS=off mematikan listener UDP built-in (misal jika FreeRADIUS memakai 1812)
GOBILL_RADIUS=:1812
GOBILL_SECRET_KEY=$KEY
EOF
		fi
		rm -f "$CONF.tmp"
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
Description=gobill billing server
Documentation=https://github.com/frand-kod/gobill
After=time-sync.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=gobill
Group=gobill
EnvironmentFile=/etc/gobill/config.env
ExecStart=/usr/local/bin/gobill
StateDirectory=gobill
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/gobill
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
systemctl enable --now gobill

log "terpasang: $("$BIN" --version 2>/dev/null || echo tidak-diketahui)"
echo "Jika ini instalasi baru, password admin pertama ada di file berikut (hapus setelah ganti password):"
echo "  $STATE_DIR/initial-admin-password.txt"
