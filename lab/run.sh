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
		# group is not "gobill-api": the provisioning script owns that name and removes it
		echo "+/user/group/add =name=lab-api =policy=api,read,write,sensitive"
		echo "+/user/add =name=gobill-api =group=lab-api =password=$API_PASS"
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

# secs "1m5s" -> 65 (RouterOS durations: w d h m s)
secs() { awk -v d="$1" 'BEGIN{n=0;while(match(d,/^[0-9]+[wdhms]/)){v=substr(d,1,RLENGTH-1)+0;u=substr(d,RLENGTH,1);n+=v*(u=="w"?604800:u=="d"?86400:u=="h"?3600:u=="m"?60:1);d=substr(d,RLENGTH+1)}print n}'; }
chr_field() { chr run "$@" | grep -o "$FIELD=[^ ]*" | head -1 | cut -d= -f2; }
rad01_uptime() { FIELD=uptime chr_field /ppp/active/print ?name=rad01; }
rad01_limit() { chr run /queue/simple/print ?name='<pppoe-rad01>' | grep -o 'max-limit=[^ ]*' | head -1; }
rate_is_5m() { rad01_limit | grep -q 'max-limit=5000000/5000000'; }
rate_or_dropped() { rate_is_5m || podman exec lab-client-1 test -s /tmp/pppoe.rc; }
router_form() { # port [reset]: re-save router 1 as the admin form would (direct mode host)
	gb_post /admin/routers/1 -d name=lab-chr -d host=10.89.77.11 -d "port=$1" -d username=gobill-api -d enabled=1 \
		-d coverage=0 ${2:+-d reset_pin=1}
}
router_pin() { gb_get /admin/routers/1/edit | grep -oE '[0-9a-f]{64}' | head -1; }
cert_ready() { chr run /certificate/print "?name=$1" | grep -q 'private-key=true'; }
pin_changed() { local p; p=$(router_pin) && [ -n "$p" ] && [ "$p" != "$1" ]; }
chr_new_cert() { # name: self-signed cert served on api-ssl
	chr run /certificate/add "=name=$1" =common-name=lab-chr >/dev/null
	chr run /certificate/sign "=number=$1" >/dev/null 2>&1 || true
	wait_for 30 cert_ready "$1"
	chr run /ip/service/set =numbers=api-ssl "=certificate=$1" =disabled=no >/dev/null
}

radplan_bw() { # bandwidth id: re-save RADIUS plan 3 (lab-rad) with that bandwidth; gobill sends CoA to its online users
	gb_post /admin/plans/3 -d name=lab-rad -d bandwidth_id="$1" -d price=200000 -d type=PPPoE -d billing=prepaid -d validity=30 \
		-d validity_unit=Days -d device=Radius -d enabled=1
}

fup_plan() { # re-save RADIUS plan 3 with a 1 MB data limit and FUP bandwidth 1 (5M), or with no limit (arg: off)
	local lim=(-d limited=1 -d limit_type=Data_Limit -d data_limit=1 -d data_unit=MB -d fup_bandwidth_id=1)
	[ "${1:-}" = off ] && lim=()
	gb_post /admin/plans/3 -d name=lab-rad -d bandwidth_id=3 -d price=200000 -d type=PPPoE -d billing=prepaid -d validity=30 \
		-d validity_unit=Days -d device=Radius -d enabled=1 "${lim[@]}"
}
# gobill_sql STMT: run one statement on the gobill database (lab-seed -sql, same binary as the seeder)
gobill_sql() { podman exec lab-gobill lab-seed -db /data/gobill.db -sql "$1"; }
rate_is() { rad01_limit | grep -q "max-limit=$1"; }
client_alive() { ! podman exec "lab-client-$1" test -s /tmp/pppoe.rc; }
snap() { chr run /ppp/profile/print; chr run /ppp/secret/print; }
push_finished() { gb_get /admin/routers/1/sync | grep -q Finished; }
push_cell() { # label: value of that row on the push status page
	gb_get /admin/routers/1/sync | tr -d ' \t\n' | grep -oE "$1</td><td>[^<]*" | head -1 | sed 's/.*<td>//'
}
chr_both_missing() { chr_lacks name=lab-g10 /ppp/profile/print && chr_lacks name=cust13 /ppp/secret/print; }

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

	echo "-- 8 plan change while online: CoA, else Disconnect fallback"
	# Editing plan lab-rad (20M -> 5M) sends CoA-Request with Mikrotik-Rate-Limit to rad01's NAS; RouterOS
	# applies it in place. If the CoA fails, gobill falls back to Disconnect-Request and the redial carries
	# the new rate. Both outcomes are accepted below.
	if [ "$LAB_MODE" = direct ]; then
		radplan_bw 3 >/dev/null   # start from lab-20M
		client_start 1 rad01 pw-rad01
		check "rad01 session up" wait_for 40 log_has 1 'session up'
		check "rad01 on CHR at 20M" wait_for 20 chr_grep 'name=rad01' /ppp/active/print
		sleep 5
		local up0 up1
		up0=$(rad01_uptime || true)
		echo "     before: uptime=$up0 $(rad01_limit || true)"
		radplan_bw 1 >/dev/null   # lab-20M -> lab-5M
		wait_for 25 rate_or_dropped || true
		if rate_is_5m; then
			up1=$(rad01_uptime || true)
			echo "     CoA applied: uptime $up0 -> $up1, $(rad01_limit)"
			check "rate changed in place, uptime did not reset" [ -n "$up1" ] && [ "$(secs "$up1")" -ge "$(secs "$up0")" ]
		else
			echo "     CoA not applied: checking the Disconnect fallback"
			check "fallback Disconnect dropped the client (PADT)" wait_for 20 rc_is 1 10
			check "gobill logged CoA failure" bash -c "podman logs lab-gobill 2>&1 | grep -q 'CoA failed, disconnecting'"
			client_start 1 rad01 pw-rad01
			check "redial gets the new rate (5M)" wait_for 40 rate_is_5m
		fi
		podman logs lab-gobill 2>&1 | grep -i 'CoA' | tail -3 | sed 's/^/     gobill: /' || true
		radplan_bw 3 >/dev/null
		podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true
	else
		echo "skip (direct mode only)"
	fi

	echo "-- 9 API-SSL certificate pin"
	if [ "$LAB_MODE" = direct ]; then
		local pin1
		chr_new_cert lab-api1
		router_form 8729 >/dev/null
		check "first TLS connect succeeds" post_grep 'Connection successful: lab-chr (lab-chr)' /admin/routers/1/test
		pin1=$(router_pin || true)
		check "fingerprint pinned" [ -n "$pin1" ]
		chr_new_cert lab-api2
		check "changed certificate refused" post_grep 'does not match the pinned one' /admin/routers/1/test
		router_form 8729 reset >/dev/null
		check "after reset: connects and re-pins" post_grep 'Connection successful' /admin/routers/1/test
		check "new fingerprint differs" pin_changed "$pin1"
		router_form 8728 >/dev/null
	else
		echo "skip (direct mode only)"
	fi

	echo "-- 10 FUP: plan speed drops past the data limit, session stays up"
	if [ "$LAB_MODE" = direct ]; then
		fup_plan >/dev/null   # 1 MB limit, FUP 5M; normal 20M
		client_start 1 rad01 pw-rad01
		check "rad01 session up" wait_for 40 log_has 1 'session up'
		check "rad01 at the plan rate (20M) within the limit" wait_for 20 rate_is 20000000/20000000
		sleep 3
		local fup0 fup1
		fup0=$(rad01_uptime || true)
		# no data flows in the lab, so the used volume is injected as an earlier closed session of rad01
		gobill_sql "INSERT INTO radius_sessions(session_id,username,nas_ip,started_at,updated_at,stopped_at,input_octets,output_octets) VALUES ('lab-old','rad01','$NAS_IP',strftime('%s','now'),strftime('%s','now'),strftime('%s','now'),1000000,4000000)"
		echo "     5 MB used of 1 MB; waiting for the FUP job (runs every minute)"
		wait_for 100 rate_is 5000000/5000000 || true
		fup1=$(rad01_uptime || true)
		echo "     uptime $fup0 -> $fup1, $(rad01_limit || true)"
		check "rate became the FUP rate (5M)" rate_is 5000000/5000000
		check "session still on the CHR" chr_grep 'name=rad01' /ppp/active/print
		check "client not disconnected" client_alive 1
		check "uptime did not reset (CoA, no redial)" [ -n "$fup1" ] && [ "$(secs "$fup1")" -ge "$(secs "$fup0")" ]
		podman logs lab-gobill 2>&1 | grep -i 'FUP' | tail -3 | sed 's/^/     gobill: /' || true
		podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true
		wait_for 10 rc_is 1 0 || true
		client_start 1 rad01 pw-rad01
		check "redial over the limit gets the FUP rate (Access-Accept)" wait_for 40 rate_is 5000000/5000000
		podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true
		fup_plan off >/dev/null
		gobill_sql "DELETE FROM radius_sessions WHERE session_id='lab-old'"
	else
		echo "skip (direct mode only)"
	fi

	echo "-- 11 bulk push (Push all) to an API-mode router"
	if [ "$LAB_MODE" = direct ]; then
		# take a plan profile and a customer secret away from the CHR, then let the push restore them
		chr run /ppp/secret/remove =numbers=cust13 >/dev/null 2>&1 || true
		chr run /ppp/profile/remove =numbers=lab-g10 >/dev/null 2>&1 || true
		check "precondition: lab-g10 and cust13 missing on CHR" chr_both_missing
		check "push starts" gb_post /admin/routers/1/sync
		check "push finished" wait_for 120 push_finished
		echo "     succeeded=$(push_cell Succeeded) failed=$(push_cell Failed) progress=$(push_cell Progress)"
		check "plan lab-g10 pushed (with burst)" chr_grep 'rate-limit=10M/10M 20M/20M 15M/15M 8' /ppp/profile/print ?name=lab-g10
		check "active customer cust13 pushed" chr_grep 'password=pw-cust13' /ppp/secret/print ?name=cust13
		check "no failures in the push" [ "$(push_cell Failed)" = 0 ]
		check "push did work (succeeded > 0)" [ "$(push_cell Succeeded)" -gt 0 ]
		local s1 s2; s1=$(snap)
		gb_post /admin/routers/1/sync >/dev/null
		sleep 2; check "second push finished" wait_for 120 push_finished
		s2=$(snap)
		check "second push changed nothing on the CHR" [ "$s1" = "$s2" ]
		check "second push has no failures" [ "$(push_cell Failed)" = 0 ]
	else
		echo "skip (direct mode only)"
	fi

	echo
	echo "$PASS passed, $FAIL failed ($(($(date +%s) - t0))s)"
	[ "$FAIL" = 0 ]
}

# ---- provision test: built-in WireGuard + one-line router provisioning (direct mode, fresh lab) ----
# ros CMD: RouterOS CLI over ssh. After provisioning the API service only answers the tunnel address,
# so chrctl (API) cannot be used any more.
# RouterOS ssh sometimes refuses a correct password when logins come fast (sshpass exit 5): retry, the command never ran.
ros() { local rc i; for i in 1 2 3 4 5 6 7 8 9 10; do
	podman exec lab-chr sh -c 'SSHPASS="$1" sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "admin@$(cat /tmp/chr.ip)" "$2"' sh "$CHR_PASS" "$1" && return 0
	rc=$?; [ $rc = 5 ] || return $rc; sleep 1; done; return $rc; }
ros_put() { local rc i; for i in 1 2 3 4 5 6 7 8 9 10; do # FILE: copy /tmp/FILE of lab-chr to the CHR as gobill.rsc
	podman exec lab-chr sh -c 'SSHPASS="$1" sshpass -e scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "/tmp/$2" "admin@$(cat /tmp/chr.ip):gobill.rsc"' sh "$CHR_PASS" "$1" && return 0
	rc=$?; [ $rc = 5 ] || return $rc; sleep 1; done; return $rc; }
ros_grep() { local pat=$1; shift; ros "$*" | grep -- "$pat" >/dev/null; }
ros_count() { ros "$2" | grep -c -- "$1"; } # PATTERN CMD
# the router config the script owns, without counters, ids and timestamps
prov_snap() {
	local p
	for p in "/interface wireguard print terse" "/interface wireguard peers print terse" "/radius print terse" "/user print terse" \
		"/user group print terse" "/ip firewall filter print terse" "/ip service print terse" "/ip address print terse"; do
		echo "## $p"
		ros "$p" | tr -d '\r' | grep -v 'remote=\|wireguard-wg-gobill' | sed -E 's/^ *[0-9]+ +//; s/ name=peer[0-9]+/ name=peer/; s/ listen-port=[0-9]+//; s/ (last-[a-z-]+|current-endpoint-[a-z]+|rx|tx)=([0-9]{4}-[0-9-]+ [0-9:]+|[^ ]*)//g'
	done
}
not() { ! "$@"; }
counts_ok() { [ "$(ros_count wg-gobill '/interface wireguard print terse')" = 1 ] && [ "$(ros_count 'name=gobill ' '/user print terse')" = 1 ] && [ "$(ros_count comment=gobill '/ip firewall filter print terse')" = 2 ]; }
stages_ok() { local o; o=$(gb_post /admin/routers/2/wg/check); grep -q '✓ Handshake: OK' <<<"$o" && grep -q '✓ API login OK' <<<"$o"; }
log_refused() { podman logs lab-gobill 2>&1 | grep "provision link refused" | grep -q "why=$1"; }
http_code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
prov_line() { # router id: POST provision, print the one-line command with a base URL the CHR can reach
	gb_post "/admin/routers/$1/wg/provision" | grep -o '/tool fetch[^<]*' | head -1 | sed "s#&\#34;#\"#g; s#&quot;#\"#g; s#http://127.0.0.1:$GOBILL_PORT#http://$GOBILL_IP:8080#"
}

test_provision() {
	[ "$LAB_MODE" = direct ] || die "provision test runs in direct mode (the CHR is reached over ssh and the lab WAN)"
	local t0; t0=$(date +%s)
	gb_login || die "cannot log in to gobill at $GOBILL"
	local tun_self=10.98.0.1 tun_peer=10.98.0.2 line fetch imp out token

	echo "-- P1 WireGuard on, router added, provisioned"
	# the lab PPP pool is 10.99.0.0/24, so the tunnel gets another subnet
	gb_post /admin/settings/wireguard -d wg_enabled=yes -d wg_port=51820 -d wg_endpoint=$GOBILL_IP -d wg_subnet=10.98.0.0/24 >/dev/null
	check "tunnel running in gobill" wait_for 10 get_grep 'Running, listening on UDP 51820' /admin/settings/wireguard
	gb_post /admin/routers -d name=prov-chr -d host=10.89.77.11 -d port=8728 -d username=gobill-api -d password="$API_PASS" -d enabled=1 -d coverage=0 >/dev/null
	check "router 2 added" get_grep 'routers/2/edit' /admin/routers
	line=$(prov_line 2)
	echo "     $line" | sed -E 's#(provision/[0-9]+-)[0-9a-f]+#\1<token>#'
	check "router page shows /tool fetch ... ; /import gobill.rsc" [ -n "$line" ]
	fetch=${line%%;*} imp=${line#*; }
	token=$(sed -E 's#.*provision/([0-9]+-[0-9a-f]+).*#\1#' <<<"$line")

	echo "-- P2 run the line on the CHR"
	# A fresh CHR has no firewall rules; the script's place-before=0 needs one. PROV_FW_RULE=1 adds a plain rule first.
	[ -z "${PROV_FW_RULE:-}" ] || ros '/ip firewall filter add chain=forward action=passthrough comment=lab-existing' >/dev/null
	out=$(ros "$fetch" 2>&1 || true)
	check "/tool fetch finished" grep -q 'status: finished' <<<"$out"
	ros ':put [/file get gobill.rsc contents]' | tr -d '\r' | podman exec -i lab-chr sh -c 'cat >/tmp/prov.rsc' # kept to run the script a second time
	out=$(ros "$imp" 2>&1 || true)
	check "/import ran without a script error" not grep -q "Script Error" <<<"$out"
	[ -z "$out" ] || echo "     import: $out"
	check "script removed gobill.rsc from the CHR" not ros_grep 'name=gobill.rsc' /file print terse
	check "wg-gobill interface" ros_grep 'name=wg-gobill' /interface wireguard print terse
	check "wg-gobill address $tun_peer" ros_grep "address=$tun_peer/24" /ip address print terse
	check "user gobill in group gobill-api" ros_grep 'name=gobill group=gobill-api' /user print terse
	check "group gobill-api has api and no ssh" ros_grep 'name=gobill-api policy=.*api.*!ssh' /user group print terse
	check "/radius entry with comment gobill, address $tun_self" ros_grep "comment=gobill .*address=$tun_self " /radius print terse
	check "/radius src-address is the tunnel address" ros_grep "src-address=$tun_peer" /radius print terse
	check "2 firewall rules tagged gobill (CoA, API)" [ "$(ros_count 'comment=gobill' '/ip firewall filter print terse')" = 2 ]
	check "api service restricted to $tun_self/32" ros_grep "name=api .*available-from=$tun_self/32" /ip service print terse
	# the seeded RADIUS entry goes, so RADIUS can only reach gobill through the tunnel
	ros "/radius remove [find address=$GOBILL_IP]" >/dev/null 2>&1 || true
	check "only the tunnel RADIUS entry is left" [ "$(ros_count 'address=' '/radius print terse')" = 1 ]

	echo "-- P3 PPPoE login, stage page, Disconnect through the tunnel"
	check "gobill Host of router 2 is the tunnel address" get_grep "value=\"$tun_peer\"" /admin/routers/2/edit
	client_start 1 rad01 pw-rad01
	check "lab-client-1 session up (RADIUS over the tunnel)" wait_for 40 log_has 1 'session up'
	check "CHR /ppp/active has rad01" ros_grep 'name=rad01' /ppp/active print terse
	check "gobill recorded accounting (session rad01)" wait_for 30 get_grep '"user":"rad01"' /admin/radius/sessions.json
	out=$(gb_post /admin/routers/2/wg/check)
	echo "$out" | grep -E '^ *(✓|✗)' | sed 's/^ */     /'
	check "stage: handshake OK" grep -q '✓ Handshake: OK' <<<"$out"
	check "stage: API login OK" grep -q '✓ API login OK' <<<"$out"
	check "stage: RADIUS packet seen" grep -q '✓ RADIUS: last packet' <<<"$out"
	check "stage: CoA reply received" grep -q '✓ CoA reply received' <<<"$out"
	check "gobill Ping to router 2 over the tunnel" post_grep 'Connection successful' /admin/routers/2/test
	local sid
	sid_of_rad01() { gb_get /admin/radius/sessions.json | grep -o '"id":[0-9]*,"user":"rad01"' | sed 's/^"id":\([0-9]*\),.*/\1/' | head -1 | grep .; }
	wait_for 20 sid_of_rad01 || true
	sid=$(sid_of_rad01 || true)
	gb_post "/admin/radius/sessions/${sid:-0}/disconnect" >/dev/null || true
	check "Disconnect over the tunnel: client dropped (PADT)" wait_for 20 rc_is 1 10
	check "rad01 gone from CHR /ppp/active" not ros_grep 'name=rad01' /ppp/active print terse
	podman exec lab-client-1 pkill pppoe >/dev/null 2>&1 || true

	echo "-- P4 the script is safe to run again"
	prov_snap >"$BUILD/prov-a.txt"
	ros_put prov.rsc
	out=$(ros "$imp" 2>&1 || true)
	check "second /import ran without a script error" not grep -q "Script Error" <<<"$out"
	[ -z "$out" ] || echo "     import: $out"
	prov_snap >"$BUILD/prov-b.txt"
	check "config identical before and after (wireguard, radius, user, firewall, services, addresses)" diff "$BUILD/prov-a.txt" "$BUILD/prov-b.txt"
	diff "$BUILD/prov-a.txt" "$BUILD/prov-b.txt" | sed 's/^/     /' || true
	check "still 1 interface, 1 gobill user, 2 rules" counts_ok
	gb_post /admin/routers/2/wg/reset >/dev/null || true
	check "tunnel back up after the second run (handshake, API login)" wait_for 60 stages_ok

	echo "-- P5 token is single use and expires"
	check "second fetch of the used URL is refused (410)" [ "$(http_code "$GOBILL/provision/$token")" = 410 ]
	check "gobill logged why=used" log_refused used
	check "a made-up token is refused (410)" [ "$(http_code "$GOBILL/provision/2-$(printf '0%.0s' $(seq 64))")" = 410 ]
	gb_post /admin/routers -d name=prov-exp -d host=10.89.77.250 -d port=8728 -d username=x -d password=x -d enabled=1 -d coverage=0 >/dev/null
	line=$(prov_line 3)
	token=$(sed -E 's#.*provision/([0-9]+-[0-9a-f]+).*#\1#' <<<"$line")
	podman run --rm --user 0 --network none -v lab-gobill-data:/data --entrypoint /usr/local/bin/lab-seed lab-gobill -db /data/gobill.db -expire-token 3 >/dev/null
	check "an expired token is refused (410)" [ "$(http_code "$GOBILL/provision/$token")" = 410 ]
	check "gobill logged why=expired" log_refused expired

	echo
	echo "$PASS passed, $FAIL failed ($(($(date +%s) - t0))s)"
	[ "$FAIL" = 0 ]
}

case "${1:-}" in
up) up ;;
test) test_lab ;;
provision) test_provision ;;
shell)
	podman exec -it lab-chr sh -c 'SSHPASS="$1" sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -tt "admin@$(cat /tmp/chr.ip)"' sh "$CHR_PASS" ;;
down) down ;;
*) sed -n '2,15p' "$0"; exit 2 ;;
esac
