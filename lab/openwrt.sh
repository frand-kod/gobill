#!/usr/bin/env bash
# OpenWrt lab: gobill runs on this host (no container), OpenWrt + uspot runs under qemu in one container,
# and one small container is the hotspot client. Tests the NAS type "wispr" (docs/en/openwrt.md).
#
#   lab/openwrt.sh up      download and verify OpenWrt, start gobill, boot and configure OpenWrt, start the client
#   lab/openwrt.sh test    login, speed, accounting, CoA -> Disconnect, quota, isolation (exit != 0 on failure)
#   lab/openwrt.sh down    stop gobill, remove the containers and the network (lab/.cache is kept)
#
# Topology: host gobill :1812-1813 <- container lab-owrt (192.168.77.1 NAT) <- OpenWrt wan 192.168.77.2
#           OpenWrt lan br-lan 10.0.0.1/22 (uspot) <- lab-owrt-lan network <- container lab-owrt-client
# Needs: podman, go, curl, python3, /dev/kvm, /dev/net/tun; UDP 1812-1813 and 3799 free on the host.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
LAB=$ROOT/lab CACHE=$ROOT/lab/.cache STATE=$ROOT/lab/.cache/owrt
OWRT_VERSION=25.12.6
OWRT_SHA=f8538784d0288b4133a99ac2a68443e4922ffde6343e80df56a5882364b9d499 # generic-ext4-combined.img.gz, from the release sha256sums
RADCLI_REF=1.3.0 USPOT_REF=87080bfa # the versions OpenWrt $OWRT_VERSION ships
SECRET=Lab-OpenWrt-Secret-2026 ADMIN_PASS=labpass123

log() { echo ">> $*" >&2; }
die() { echo "!! $*" >&2; exit 1; }
ssh_opts=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5)
# ow CMD: run CMD on OpenWrt over its WAN address (stdin is passed on)
ow() { podman exec -i lab-owrt ssh "${ssh_opts[@]}" root@192.168.77.2 "$@"; }
cl() { podman exec lab-owrt-client sh -c "$1"; }
ctl() { (cd "$ROOT" && go run ./lab/owrtctl -db "$STATE/gobill.db" "$@"); }
q() { python3 -I -c 'import sqlite3,sys; print(*sqlite3.connect(sys.argv[1]).execute(sys.argv[2]).fetchone() or ["-"])' "$STATE/gobill.db" "$1"; }
wait_for() { local t=$1; shift; for _ in $(seq "$t"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

up() {
	podman container exists lab-owrt && die "lab already up: lab/openwrt.sh down first"
	mkdir -p "$CACHE"
	local img=$CACHE/owrt-$OWRT_VERSION.img gz=$CACHE/openwrt-$OWRT_VERSION-x86-64-generic-ext4-combined.img.gz
	if [ ! -s "$img" ]; then
		log "download OpenWrt $OWRT_VERSION"
		curl -fsSL -o "$gz" "https://downloads.openwrt.org/releases/$OWRT_VERSION/targets/x86/64/$(basename "$gz")"
		echo "$OWRT_SHA  $gz" | sha256sum -c --quiet || die "OpenWrt image checksum mismatch"
		gunzip -c "$gz" >"$img" 2>/dev/null || [ -s "$img" ] # the image carries trailing padding gunzip warns about
	fi
	podman build -q -t lab-owrt -f "$LAB/owrt.Containerfile" "$LAB" >/dev/null

	log "gobill on the host"
	rm -rf "$STATE" && mkdir -p "$STATE/vm" "$STATE/radcli"
	(cd "$ROOT" && go build -o "$STATE/gobill" ./cmd/gobill)
	ctl -admin-pass "$ADMIN_PASS" -nas-secret "$SECRET" seed
	GOBILL_DB=$STATE/gobill.db GOBILL_HTTP=127.0.0.1:8090 GOBILL_RADIUS=0.0.0.0:1812 GOBILL_HTTPS=0 \
		GOBILL_BACKUP_DIR=$STATE/backup nohup "$STATE/gobill" >"$STATE/gobill.log" 2>&1 </dev/null &
	sleep 1; pgrep -f "^$STATE/gobill" >/dev/null && wait_for 10 curl -fs http://127.0.0.1:8090/health || die "gobill did not start, see $STATE/gobill.log"

	log "OpenWrt boot"
	podman network exists lab-owrt-lan || podman network create --subnet 10.89.79.0/24 --internal --disable-dns lab-owrt-lan >/dev/null
	podman run -d --name lab-owrt --user 0 --memory 400m --network podman --network lab-owrt-lan \
		--cap-add NET_ADMIN --cap-add NET_RAW --device /dev/kvm --device /dev/net/tun --security-opt label=disable \
		--sysctl net.ipv4.ip_forward=1 -p 3799:3799/udp \
		-v "$img:/owrt/base.img:ro" -v "$STATE/vm:/owrt/vm" lab-owrt >/dev/null
	# a fresh OpenWrt answers ssh on its LAN 192.168.1.1, without a password
	wait_for 30 podman exec lab-owrt ip link show br-lan || die "container did not start"
	podman exec lab-owrt ip addr add 192.168.1.250/24 dev br-lan
	lanssh() { podman exec -i lab-owrt ssh "${ssh_opts[@]}" root@192.168.1.1 "$@"; }
	wait_for 90 lanssh true || die "OpenWrt ssh not up, see /tmp/owrt-console.log in lab-owrt"

	log "uspot (packages, RADIUS dictionary, captive network)"
	curl -fsSL "https://raw.githubusercontent.com/radcli/radcli/$RADCLI_REF/etc/dictionary" >"$STATE/radcli/dictionary"
	for f in dictionary.WISPr dictionary.chillispot; do
		curl -fsSL "https://raw.githubusercontent.com/f00b4r0/uspot/$USPOT_REF/files/etc/radcli/$f" >"$STATE/radcli/$f"
	done
	printf '$INCLUDE /etc/radcli/dictionary.WISPr\n$INCLUDE /etc/radcli/dictionary.chillispot\n' >>"$STATE/radcli/dictionary"
	lanssh "mkdir -p /etc/radcli"
	for f in dictionary dictionary.WISPr dictionary.chillispot; do lanssh "cat >/etc/radcli/$f" <"$STATE/radcli/$f"; done
	lanssh "cat >/tmp/setup.sh" <"$LAB/owrt-uspot.sh"
	lanssh "sh /tmp/setup.sh '$SECRET' </dev/null" || true # ends in a reboot
	sleep 5
	wait_for 120 ow "pgrep -f uspot.uc" || die "uspot not running after the reboot"

	log "client"
	podman run -d --name lab-owrt-client --network lab-owrt-lan --cap-add NET_ADMIN --cap-add NET_RAW \
		docker.io/library/alpine:3.22 sh -c "trap 'exit 0' TERM; while :; do sleep 1; done" >/dev/null
	cl "ip addr flush dev eth0; udhcpc -i eth0 -n -q -t 10 >/dev/null 2>&1"
	# download source for the speed tests, on the WAN side of OpenWrt
	podman exec lab-owrt sh -c 'mkdir -p /www && head -c 1500000 /dev/urandom >/www/big'
	podman exec -d lab-owrt sh -c 'while :; do { printf "HTTP/1.0 200 OK\r\nContent-Length: 1500000\r\n\r\n"; cat /www/big; } | nc -l -p 8000 -s 192.168.77.1 >/dev/null; done'
	cat >&2 <<-EOF

	OpenWrt lab up
	  gobill UI   http://127.0.0.1:8090  admin / $ADMIN_PASS   (log: $STATE/gobill.log)
	  OpenWrt     podman exec -it lab-owrt ssh root@192.168.77.2
	  client      podman exec -it lab-owrt-client sh   (10.0.0.0/22, portal http://10.0.0.1/hotspot)
	EOF
}

fails=0
check() { local name=$1; shift; if "$@"; then echo "ok   $name"; else echo "FAIL $name"; fails=$((fails + 1)); fi; }
login() { cl "wget -qO- --post-data 'action=radius&username=$1&password=pw-$1' http://10.0.0.1/hotspot" | grep -q Connected; }
online() { ow "ubus call uspot client_list" | grep -Eq '"([0-9a-f]{2}:){5}'; }
# rate_ok LIMIT_BPS: download 1.5 MB, true when the rate is 80..105 % of the limit
rate_ok() {
	local s e
	s=$(date +%s%N); cl "wget -qO /dev/null http://192.168.77.1:8000/big"; e=$(date +%s%N)
	python3 -I -c 'import sys; r=12e6/((int(sys.argv[2])-int(sys.argv[1]))/1e9); print("     %.2f Mbit/s" % (r/1e6)); sys.exit(not 0.8*int(sys.argv[3]) <= r <= 1.05*int(sys.argv[3]))' "$s" "$e" "$1"
}
kick() { ctl coa "$1" | grep -q 'disconnect <nil>'; }
refused() { ! login "$1"; }
offline() { ! online; }
logged() { ow logread | grep -q "$1"; }
last_auth() { [ "$(q "SELECT accepted||' '||reason FROM radius_auth_log WHERE username='$1' ORDER BY id DESC LIMIT 1")" = "$2" ]; }

test_() {
	podman container exists lab-owrt || die "lab is not up"
	check "login hs01 through uspot" login hs01
	check "ratelimit set from WISPr (4M down, 1M up)" logged 'setting .* e:4000000 i:1000000'
	check "download near 4 Mbit/s" rate_ok 4000000
	sleep 2
	check "accounting: open session with NAS-Identifier" [ "$(q "SELECT nas_identifier FROM radius_sessions WHERE username='hs01' AND stopped_at IS NULL")" = lab-owrt ]
	ctl sql "UPDATE bandwidths SET rate_down=1, rate_down_unit='Mbps', rate_up=512, rate_up_unit='Kbps' WHERE id=1"
	check "CoA NAKed, Disconnect accepted" kick hs01
	check "client removed" wait_for 5 offline
	check "relogin gets the new plan speed" login hs01
	check "download near 1 Mbit/s" rate_ok 1000000
	kick hs01 || true

	check "login hs02 (5 MB quota)" login hs02
	for _ in 1 2 3 4 5; do cl "wget -T 10 -qO /dev/null http://192.168.77.1:8000/big" || true; done
	sleep 2
	check "uspot ends the session at the quota" wait_for 60 logged 'max total octets reached'
	check "relogin refused: quota used" refused hs02
	check "gobill logged reason limit" last_auth hs02 "0 limit"

	check "isolated hs03 refused" refused hs03
	check "gobill logged reason isolated" last_auth hs03 "0 isolated"
	[ "$fails" = 0 ] || die "$fails check(s) failed"
	log "all checks passed"
}

down() {
	pkill -f "^$STATE/gobill" || true
	podman rm -f lab-owrt lab-owrt-client >/dev/null 2>&1 || true
	podman network rm lab-owrt-lan >/dev/null 2>&1 || true
	log "down"
}

case "${1:-}" in
up) up ;;
test) test_ ;;
down) down ;;
*) die "usage: lab/openwrt.sh up|test|down" ;;
esac
