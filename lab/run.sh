#!/usr/bin/env bash
# Local test lab: gobill (container, run like a service) + MikroTik RouterOS CHR (qemu/KVM in a
# container) + PPPoE clients, all on rootless podman. See the Components block below.
#
#   lab/run.sh up      build images, create networks and containers, seed CHR and gobill
#   lab/run.sh test    smoke test (exit != 0 on failure)
#   lab/run.sh shell   RouterOS console on the CHR
#   lab/run.sh down    remove containers, networks, volume (lab/.cache is kept)
#
# Env: CHR_VERSION (7.24.5), CHR_PASS (random, printed), CLIENTS (2), LAB_MODE (direct|nat),
#      GOBILL_PORT (8090, host port of the gobill UI), GOBILL_ADMIN_PASS (random).
#   direct: gobill reaches the CHR API (8728) on lab-wan.
#   nat:    the CHR sits behind a NAT box; it can call gobill, gobill cannot call it.
#
# Topology:  lab-wan: lab-gobill <-- CHR ether1      lab-lan: CHR ether2 (PPPoE server) <-- lab-client-N
# Needs: podman, go, curl, unzip, /dev/kvm, /dev/net/tun.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
LAB=$ROOT/lab CACHE=$LAB/.cache BUILD=$LAB/.cache/build STATE=$LAB/.cache/lab.env
CHR_VERSION=${CHR_VERSION:-7.24.5}
# SHA256 of chr-$CHR_VERSION.img.zip, as published at download.mikrotik.com/routeros/$CHR_VERSION/chr-$CHR_VERSION.img.zip.sha256
declare -A CHR_SHA=([7.24.5]=16f07222a3213352c9c4fbea4f4dec6a8ffb78fdc0fa5e398c23103ed357c6eb)
CLIENTS=${CLIENTS:-2} LAB_MODE=${LAB_MODE:-direct} GOBILL_PORT=${GOBILL_PORT:-8090}
WAN_NET=10.89.77.0/24 LAN_NET=10.89.78.0/24 GOBILL_IP=10.89.77.2 CHR_IP=10.89.77.10
[ -f "$STATE" ] && [ "${1:-}" != up ] && . "$STATE"   # up writes the generated secrets
GOBILL=http://127.0.0.1:$GOBILL_PORT
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

log() { echo ">> $*" >&2; }
die() { echo "!! $*" >&2; exit 1; }
chr_api() { echo "$(podman exec lab-chr cat /tmp/chr.ip):8728"; }
# chr CMD...: chrctl against the CHR API, executed inside the lab-chr container
chr() { podman exec lab-chr chrctl -a "$(chr_api)" -u admin -p "$CHR_PASS" "$@"; }
chr_script() { podman exec -i lab-chr chrctl -a "$(chr_api)" -u "${CHR_USER:-admin}" -p "${CHR_USER_PASS:-$CHR_PASS}" script; }

# wait_for SECONDS cmd...
wait_for() { local t=$1; shift; for _ in $(seq "$t"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

down() {
	for c in $(podman ps -a --format '{{.Names}}' | grep '^lab-' || true); do podman rm -f -t 2 "$c" >/dev/null; done
	podman volume rm -f lab-gobill-data >/dev/null 2>&1 || true
	for n in lab-wan lab-lan; do podman network rm -f "$n" >/dev/null 2>&1 || true; done
	rm -f "$STATE"
}

fetch_chr() {
	local zip=$CACHE/chr-$CHR_VERSION.img.zip want=${CHR_SHA[$CHR_VERSION]:-}
	[ -n "$want" ] || die "no pinned SHA256 for CHR $CHR_VERSION: add it to CHR_SHA (see .../chr-$CHR_VERSION.img.zip.sha256)"
	mkdir -p "$CACHE"
	[ -f "$zip" ] || { log "download CHR $CHR_VERSION"; curl -fL -sS -C - --retry 5 -o "$zip" "https://download.mikrotik.com/routeros/$CHR_VERSION/chr-$CHR_VERSION.img.zip"; }
	[ "$(sha256sum "$zip" | cut -d' ' -f1)" = "$want" ] || die "SHA256 mismatch for $zip (expected $want)"
	[ -f "$CACHE/chr-$CHR_VERSION.img" ] || unzip -oq "$zip" -d "$CACHE" "chr-$CHR_VERSION.img"
}

build() {
	log "build binaries and images"
	mkdir -p "$BUILD"
	for p in gobill:./cmd/gobill seed:./lab/seed chrctl:./lab/chrctl pppoe:./lab/pppoe; do
		CGO_ENABLED=0 go build -C "$ROOT" -trimpath -ldflags "-s -w" -o "$BUILD/${p%%:*}" "${p#*:}"
	done
	cp "$LAB"/chr-entry.sh "$BUILD/"
	podman build -q -t lab-chr -f "$LAB/chr.Containerfile" "$BUILD" >/dev/null
	podman build -q -t lab-gobill -f "$LAB/gobill.Containerfile" "$BUILD" >/dev/null
	podman build -q -t lab-client -f "$LAB/client.Containerfile" "$BUILD" >/dev/null
}

up() {
	[ "$LAB_MODE" = direct ] || [ "$LAB_MODE" = nat ] || die "LAB_MODE must be direct or nat"
	[ -z "$(podman ps -a --format '{{.Names}}' | grep '^lab-' || true)" ] || die "lab already exists: run 'lab/run.sh down' first"
	fetch_chr
	build
	local pw; pw() { head -c 18 /dev/urandom | base64 | tr -d '+/=' | head -c 14; }
	CHR_PASS=${CHR_PASS:-$(pw)} API_PASS=$(pw) RADIUS_SECRET=$(pw) GOBILL_ADMIN_PASS=${GOBILL_ADMIN_PASS:-$(pw)}
	if [ "$LAB_MODE" = direct ]; then NAS_IP=10.89.77.11 ROUTER_HOST=10.89.77.11; else NAS_IP=$CHR_IP ROUTER_HOST=192.168.77.2; fi
	umask 077
	printf '%s\n' "CHR_PASS=$CHR_PASS" "API_PASS=$API_PASS" "RADIUS_SECRET=$RADIUS_SECRET" "GOBILL_ADMIN_PASS=$GOBILL_ADMIN_PASS" \
		"LAB_MODE=$LAB_MODE" "CLIENTS=$CLIENTS" "GOBILL_PORT=$GOBILL_PORT" "NAS_IP=$NAS_IP" >"$STATE"
	umask 022

	log "networks, gobill (mode $LAB_MODE)"
	podman network create --subnet "$WAN_NET" lab-wan >/dev/null
	podman network create --subnet "$LAN_NET" --internal lab-lan >/dev/null
	podman volume create lab-gobill-data >/dev/null
	podman run --rm --user 0 --network none -v lab-gobill-data:/data --entrypoint /usr/local/bin/lab-seed lab-gobill \
		-db /data/gobill.db -admin-pass "$GOBILL_ADMIN_PASS" -router-host "$ROUTER_HOST" -router-pass "$API_PASS" \
		-nas-ip "$NAS_IP" -nas-secret "$RADIUS_SECRET" 2>&1 | sed 's/^/   /'
	# restart policy + healthcheck make it behave like a service: killed -> restarted, unhealthy -> flagged
	podman run -d --name lab-gobill --user 0 --network "lab-wan:ip=$GOBILL_IP" --memory 128m -p "$GOBILL_PORT:8080" \
		--restart=unless-stopped -v lab-gobill-data:/data \
		--health-cmd 'wget -qO- http://127.0.0.1:8080/health' --health-interval 5s --health-retries 3 --health-start-period 5s \
		lab-gobill >/dev/null

	log "CHR $CHR_VERSION boot"
	podman run -d --name lab-chr --user 0 --network "lab-wan:ip=$CHR_IP" --network lab-lan --memory 400m \
		--cap-add NET_ADMIN --cap-add NET_RAW --device /dev/kvm --device /dev/net/tun --security-opt label=disable \
		--sysctl net.ipv4.ip_forward=1 -e LAB_MODE="$LAB_MODE" \
		-v "$CACHE/chr-$CHR_VERSION.img:/chr/base.img:ro" lab-chr >/dev/null
	for _ in $(seq 60); do podman exec lab-chr test -s /tmp/chr.ip 2>/dev/null && break; sleep 1; done
	# the API port opens when RouterOS is up; a fresh CHR has an empty admin password
	wait_for 240 podman exec lab-chr sh -c 'nc -z "$(cat /tmp/chr.ip)" 8728' || { podman logs lab-chr | tail; die "CHR API not up"; }
	sleep 3
	chr run /system/identity/print >/dev/null 2>&1 ||
		podman exec lab-chr chrctl -a "$(chr_api)" -u admin -p "" run /user/set =numbers=admin "=password=$CHR_PASS" ||
		die "cannot log in to the CHR"
	seed_chr

	log "clients"
	for i in $(seq "$CLIENTS"); do
		podman run -d --name "lab-client-$i" --network lab-lan --memory 48m --cap-add NET_RAW lab-client >/dev/null
	done
	gobill_wait
	warm_up
	cat <<MSG

lab up (mode $LAB_MODE)
  gobill UI    $GOBILL   admin / $GOBILL_ADMIN_PASS
  CHR admin    admin / $CHR_PASS      (lab/run.sh shell)
  CHR api user gobill-api / $API_PASS
  RADIUS       NAS $NAS_IP secret $RADIUS_SECRET
  secrets saved in $STATE
MSG
}

# The very first accounting a CHR sends is Accounting-On + Start in the same millisecond. gobill can
# process Start first and then close it with Accounting-On (internal/radius/radius.go:819), and an
# Interim never reopens it (internal/db/queries/radius.sql:73): the live session shows as stopped.
# One throwaway login absorbs that so the test sees steady state. LAB_WARMUP=0 skips it, to reproduce.
warm_up() {
	[ "${LAB_WARMUP:-1}" = 0 ] && return
	log "warm-up login"
	client_start 1 rad02 pw-rad02
	wait_for 30 log_has 1 'session up' || log "warm-up login did not come up (continuing)"
	podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true
	wait_for 10 rc_is 1 0 || true
}

gobill_wait() {
	for _ in $(seq 60); do [ "$(podman inspect -f '{{.State.Health.Status}}' lab-gobill)" = healthy ] && return; sleep 1; done
	podman logs lab-gobill | tail; die "gobill not healthy"
}

# Idempotent: a "+" line becomes /set when the =name= already exists.
seed_chr() {
	log "seed CHR"
	local i
	{
		echo "/system/identity/set =name=lab-chr"
		echo "+/user/group/add =name=gobill-api =policy=api,read,write,sensitive"
		echo "+/user/add =name=gobill-api =group=gobill-api =password=$API_PASS"
		echo "+/ip/pool/add =name=gb-pool =ranges=10.99.0.10-10.99.0.250"
		echo "+/ppp/profile/add =name=chr-5m =local-address=10.99.0.1 =remote-address=gb-pool =rate-limit=5M/5M"
		echo "+/ppp/profile/add =name=chr-10m =local-address=10.99.0.1 =remote-address=gb-pool =rate-limit=10M/10M"
		echo "+/ppp/profile/add =name=chr-weird =local-address=10.99.0.1 =remote-address=gb-pool =rate-limit=2M/4M%204M/8M%203M/6M%208/8%208%201M/2M"
		echo "/ppp/aaa/set =use-radius=yes =accounting=yes =interim-update=1m"
		echo "/radius/incoming/set =accept=yes"
		local prof=(chr-5m chr-10m chr-weird)
		for i in $(seq 12); do   # cust01..12 collide with gobill; 04/05 have another password; 09/10 are disabled
			local n; n=$(printf cust%02d "$i") p=pw-$(printf cust%02d "$i") dis=no
			[ "$i" = 4 ] || [ "$i" = 5 ] && p=other-pw
			[ "$i" = 9 ] || [ "$i" = 10 ] && dis=yes
			echo "+/ppp/secret/add =name=$n =password=$p =service=pppoe =profile=${prof[i%3]} =disabled=$dis"
		done
		for i in $(seq 8); do    # legacy01..08 exist only on the CHR; 07 disabled
			local dis=no; [ "$i" = 7 ] && dis=yes
			echo "+/ppp/secret/add =name=$(printf legacy%02d "$i") =password=legacy-pw =service=pppoe =profile=${prof[i%3]} =disabled=$dis"
		done
		for i in $(seq 6); do echo "+/ip/hotspot/user/add =name=$(printf cust%02d "$i") =password=pw-$(printf cust%02d "$i")"; done
		for i in $(seq 7 20); do   # hs07..20; every 5th disabled
			local dis=no; [ $((i % 5)) = 0 ] && dis=yes
			echo "+/ip/hotspot/user/add =name=$(printf hs%02d "$i") =password=hs-pw =disabled=$dis"
		done
	} | chr_script
	# commands without a =name= key: add only when missing
	chr run /interface/pppoe-server/server/print | grep -q service-name=lab ||
		chr run /interface/pppoe-server/server/add =service-name=lab =interface=ether2 =default-profile=chr-10m \
			=authentication=pap,chap =disabled=no >/dev/null
	chr run /radius/print | grep -q "address=$GOBILL_IP " ||
		chr run /radius/add =service=ppp "=address=$GOBILL_IP" "=secret=$RADIUS_SECRET" =timeout=3s >/dev/null
}

# ---- test ----
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); echo "ok   $*"; }
bad() { FAIL=$((FAIL + 1)); echo "FAIL $*"; }
check() { local name=$1; shift; if "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi; }
expect_fail() { local name=$1; shift; if "$@" >/dev/null 2>&1; then bad "$name (succeeded, expected failure)"; else ok "$name (failed as expected)"; fi; }

gb_login() {
	curl -fs -c "$JAR" -b "$JAR" -o /dev/null -d "username=admin&password=$GOBILL_ADMIN_PASS" "$GOBILL/admin/login"
}
# gb_post PATH [curl args]: POST and follow the redirect so the flash message is in the output
gb_post() { local p=$1; shift; curl -fsL -c "$JAR" -b "$JAR" -d '' "$@" "$GOBILL$p"; }
gb_get() { curl -fsL -c "$JAR" -b "$JAR" "$GOBILL$1"; }

plan_form() { # id: re-save the plan as the admin form would, which pushes it to the router
	local id=$1
	case $id in
	1) set -- -d name=lab-g5 -d bandwidth_id=1 -d price=100000 ;;
	2) set -- -d name=lab-g10 -d bandwidth_id=2 -d price=150000 ;;
	esac
	gb_post "/admin/plans/$id" -d type=PPPoE -d billing=prepaid -d validity=30 -d validity_unit=Days -d router_id=1 -d pool_id=1 \
		-d device=MikrotikPppoe -d enabled=1 "$@"
}

client_log() { podman exec "lab-client-$1" cat /tmp/pppoe.log 2>/dev/null; }
client_start() { # n user pass
	podman exec "lab-client-$1" sh -c 'rm -f /tmp/pppoe.log /tmp/pppoe.rc; pkill pppoe || true' || true
	podman exec -d "lab-client-$1" sh -c "pppoe -i eth0 -u $2 -p $3 >/tmp/pppoe.log 2>&1; echo \$? >/tmp/pppoe.rc" >/dev/null
}
client_rc() { podman exec "lab-client-$1" cat /tmp/pppoe.rc 2>/dev/null; }

chr_grep() { local pat=$1; shift; chr run "$@" | grep -- "$pat" >/dev/null; }
chr_lacks() { ! chr_grep "$@"; }
post_grep() { local pat=$1; shift; gb_post "$@" | grep -- "$pat" >/dev/null; }
get_grep() { local pat=$1; shift; gb_get "$@" | grep -- "$pat" >/dev/null; }
log_has() { podman exec "lab-client-$1" cat /tmp/pppoe.log 2>/dev/null | grep -- "$2" >/dev/null; }
rc_is() { [ "$(podman exec "lab-client-$1" cat /tmp/pppoe.rc 2>/dev/null)" = "$2" ]; }
health_is() { [ "$(podman inspect -f '{{.State.Health.Status}}' lab-gobill)" = "$1" ]; }
restarted() { local p; p=$(podman inspect -f '{{.State.Pid}}' lab-gobill); [ "$p" != 0 ] && [ "$p" != "$1" ]; }

test_lab() {
	local t0; t0=$(date +%s)
	gb_login || die "cannot log in to gobill at $GOBILL"
	echo "mode: $LAB_MODE"

	echo "-- 1 router ping + health"
	if [ "$LAB_MODE" = direct ]; then
		check "gobill Ping to CHR" post_grep 'Connection successful: lab-chr (lab-chr)' /admin/routers/1/test
		check "gobill Health from CHR (version $CHR_VERSION)" post_grep "$CHR_VERSION" /admin/network/router/1/check
	else
		expect_fail "gobill Ping to CHR (behind NAT)" post_grep 'Connection successful' /admin/routers/1/test
	fi

	echo "-- 2 plans and customers synced to CHR"
	if [ "$LAB_MODE" = direct ]; then
		plan_form 1 >/dev/null; plan_form 2 >/dev/null
		check "profile lab-g5 on CHR" chr_grep 'rate-limit=5M/5M' /ppp/profile/print ?name=lab-g5
		check "profile lab-g10 on CHR (burst)" chr_grep 'rate-limit=10M/10M 20M/20M 15M/15M 8' /ppp/profile/print ?name=lab-g10
		gb_post /admin/customers/13/sync >/dev/null || true
		gb_post /admin/customers/4/sync >/dev/null || true
		check "cust13 (gobill only) created on CHR" chr_grep 'password=pw-cust13' /ppp/secret/print ?name=cust13
		check "cust04 password overwritten on CHR" chr_grep 'password=pw-cust04' /ppp/secret/print ?name=cust04
	else
		echo "skip (CHR unreachable from gobill in nat mode)"
	fi

	echo "-- 3 PPPoE login through RADIUS"
	client_start 1 rad01 pw-rad01
	check "lab-client-1 session up" wait_for 40 log_has 1 'session up'
	client_log 1 | sed 's/^/     /'
	check "CHR /ppp/active has rad01" chr_grep 'name=rad01' /ppp/active/print
	check "gobill recorded accounting (session rad01)" wait_for 30 get_grep '"user":"rad01"' /admin/radius/sessions.json

	echo "-- 4 Disconnect from gobill"
	local sid out
	sid_of_rad01() { gb_get /admin/radius/sessions.json | grep -o '"id":[0-9]*,"user":"rad01"' | sed 's/^"id":\([0-9]*\),.*/\1/' | head -1 | grep .; }
	wait_for 20 sid_of_rad01 || true
	sid=$(sid_of_rad01 || true)
	out=$(gb_post "/admin/radius/sessions/${sid:-0}/disconnect" || true)
	if [ "$LAB_MODE" = direct ]; then
		check "client-1 dropped by the CHR (PADT)" wait_for 20 rc_is 1 10
		check "rad01 gone from CHR /ppp/active" chr_lacks 'name=rad01' /ppp/active/print
	else
		check "Disconnect fails in nat mode (CoA cannot reach the NAS)" grep -qi 'failed' <<<"$out"
		check "session stays up" bash -c "! podman exec lab-client-1 test -s /tmp/pppoe.rc"
	fi
	podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true

	echo "-- 5 wrong password"
	client_start 2 rad02 wrong-password
	check "wrong password rejected (exit 1)" wait_for 40 rc_is 2 1
	client_log 2 | sed 's/^/     /'
	check "no rad02 session on CHR" chr_lacks 'name=rad02' /ppp/active/print

	echo "-- 6 NAT isolation"
	if [ "$LAB_MODE" = nat ]; then
		expect_fail "wan host -> NAT box:8728" podman run --rm --network lab-wan lab-client nc -z -w 3 "$CHR_IP" 8728
		expect_fail "wan host -> CHR inner IP:8728" podman run --rm --network lab-wan lab-client nc -z -w 3 192.168.77.2 8728
		check "CHR -> gobill outbound (/tool fetch)" chr_grep 'status=finished' /tool/fetch =mode=http "=url=http://$GOBILL_IP:8080/health" =output=user
	else
		echo "skip (nat mode only)"
	fi

	echo "-- 7 gobill as a service"
	check "podman healthcheck healthy" health_is healthy
	local pid1; pid1=$(podman inspect -f '{{.State.Pid}}' lab-gobill)
	kill -9 "$pid1"   # a crash; 'podman kill' counts as a manual stop and is not restarted
	check "restarted after kill" wait_for 30 restarted "$pid1"
	check "restart count is 1" [ "$(podman inspect -f '{{.RestartCount}}' lab-gobill)" = 1 ]
	check "healthy again" wait_for 40 health_is healthy
	check "data survived restart (admin login)" gb_login

	echo
	echo "$PASS passed, $FAIL failed ($(($(date +%s) - t0))s)"
	[ "$FAIL" = 0 ]
}

case "${1:-}" in
up) up ;;
test) test_lab ;;
shell)
	podman exec -it lab-chr sh -c 'SSHPASS="$1" sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -tt "admin@$(cat /tmp/chr.ip)"' sh "$CHR_PASS" ;;
down) down ;;
*) sed -n '2,15p' "$0"; exit 2 ;;
esac
