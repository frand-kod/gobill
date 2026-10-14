#!/usr/bin/env bash
# Head-to-head benchmark: PHPNuxBill (PHP 8.2 + Apache + MariaDB) vs gobill on the same host and data.
# Needs: podman, ab (httpd-tools), curl, go. Results are printed as Markdown tables.
#
#   PHPNUXBILL=../phpnuxbill bench/run.sh
#
# Env: CUSTOMERS (5000), DURATION (30 seconds per scenario), CONCURRENCY (50), CPUS (2, per container).
set -euo pipefail

PHPNUXBILL=$(realpath "${PHPNUXBILL:?set PHPNUXBILL to a PHPNuxBill checkout}")
CUSTOMERS=${CUSTOMERS:-5000} DURATION=${DURATION:-30} CONCURRENCY=${CONCURRENCY:-50} CPUS=${CPUS:-2}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
W=$(mktemp -d)
PHP=http://127.0.0.1:8081 GO=http://127.0.0.1:8082
trap 'podman rm -f bench-gobill >/dev/null 2>&1; podman pod rm -f bench-php >/dev/null 2>&1; podman unshare rm -rf "$W"' EXIT

log() { echo ">> $*" >&2; }
wait_http() { until curl -fso /dev/null "$1"; do sleep 0.1; done; }

# PSS (shared pages split fairly between processes) of every process in a container, in MB.
pss() {
	for p in $(podman top "$1" hpid | tail -n +2); do podman unshare cat "/proc/$p/smaps_rollup" 2>/dev/null; done |
		awk '/^Pss:/{s+=$2} END{printf "%.0f", s/1024}'
}
pss_php() { echo $(($(pss bench-php-web) + $(pss bench-php-db))); }

# Runs a command (its stdout goes to $W/out) and prints the highest PSS seen meanwhile.
peak() {
	local f="$W/peak" which=$1; shift
	echo 0 >"$f"
	(while :; do m=$($which); [ "$m" -gt "$(cat "$f")" ] && echo "$m" >"$f"; sleep 0.2; done) &
	local s=$!
	"$@" >"$W/out"
	kill $s; wait $s 2>/dev/null || true
	cat "$f"
}

# ab result line: "req/s | p50 ms | p99 ms | non-2xx". Dynamic pages vary in length, so ab "Failed requests" is not used.
ab_run() {
	local out
	out=$(ab -q -k -t "$DURATION" -n 10000000 -c "$CONCURRENCY" "$@" 2>&1)
	local rps p50 p99 non2xx
	rps=$(awk '/Requests per second/{printf "%.0f", $4}' <<<"$out")
	p50=$(awk '$1=="50%"{print $2}' <<<"$out")
	p99=$(awk '$1=="99%"{print $2}' <<<"$out")
	non2xx=$(awk '/Non-2xx responses/{print $3}' <<<"$out")
	echo "$rps|$p50|$p99|${non2xx:-0}"
}

log "build images"
podman build -q -t bench-phpnuxbill -f "$ROOT/bench/php.Containerfile" "$ROOT/bench" >/dev/null
CGO_ENABLED=0 go build -C "$ROOT" -trimpath -ldflags "-s -w" -o "$W/gobill" ./cmd/gobill
printf 'FROM gcr.io/distroless/static:nonroot\nCOPY gobill /gobill\nENTRYPOINT ["/gobill"]\n' >"$W/Containerfile"
podman build -q -t bench-gobill "$W" >/dev/null

log "PHPNuxBill $(grep -o '"[0-9.]*"' "$PHPNUXBILL/version.json") + MariaDB, $CUSTOMERS customers"
cp -r "$PHPNUXBILL" "$W/php"
rm -rf "$W/php/.git" "$W/php/install/index.php"
sed -e 's/"localhost"; # Database Host/"127.0.0.1"; # Database Host/' \
	-e 's/\$db_pass    = ""/$db_pass    = "bench"/' "$W/php/config.sample.php" >"$W/php/config.php"
chmod -R a+rwX "$W/php"
podman pod create --name bench-php -p 8081:80 -p 3307:3306 >/dev/null
podman run -d --pod bench-php --name bench-php-db --cpus "$CPUS" \
	-e MARIADB_ROOT_PASSWORD=bench -e MARIADB_DATABASE=phpnuxbill docker.io/library/mariadb:10.11 >/dev/null
# TCP, not the socket: the image first runs a socket-only server for init, then restarts it
until podman exec bench-php-db mariadb -h127.0.0.1 -uroot -pbench -e 'select 1' >/dev/null 2>&1; do sleep 1; done
for f in install/phpnuxbill.sql install/radius.sql; do podman exec -i bench-php-db mariadb -uroot -pbench phpnuxbill <"$W/php/$f"; done
sed "s/@CUSTOMERS@/$CUSTOMERS/" "$ROOT/bench/seed.sql" | podman exec -i bench-php-db mariadb -uroot -pbench phpnuxbill

T0=$(date +%s%N)
podman run -d --pod bench-php --name bench-php-web --cpus "$CPUS" -v "$W/php:/var/www/html:Z" bench-phpnuxbill >/dev/null
wait_http "$PHP/?_route=admin"
PHP_START=$((($(date +%s%N) - T0) / 1000000))

log "import into gobill"
mkdir -p "$W/data"
T0=$(date +%s%N)
"$W/gobill" import --mysql-dsn 'root:bench@tcp(127.0.0.1:3307)/phpnuxbill' --db "$W/data/gobill.db" >/dev/null
# Containers reach /radius.php through the rootless port forwarder, so allow every peer.
python3 - "$W/data/gobill.db" <<'EOF'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("INSERT INTO settings(key, value) VALUES('radius_rest_allow','0.0.0.0/0,::/0') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
c.commit()
EOF
IMPORT_S=$(awk -v ns=$(($(date +%s%N) - T0)) 'BEGIN{printf "%.0f", ns/1e9}')
chmod -R a+rwX "$W/data"

T0=$(date +%s%N)
podman run -d --name bench-gobill --cpus "$CPUS" -p 8082:8080 -v "$W/data:/data:Z" \
	-e GOBILL_DB=/data/gobill.db -e GOBILL_HTTP=:8080 -e GOBILL_RADIUS=off -e GOBILL_HTTPS=0 \
	-e GOBILL_BACKUP_DIR=/data/backup bench-gobill >/dev/null
wait_http "$GO/admin/login"
GO_START=$((($(date +%s%N) - T0) / 1000000))

log "admin sessions"
curl -fs -c "$W/php.cj" -b "$W/php.cj" -o /dev/null "$PHP/?_route=admin"
curl -fs -c "$W/php.cj" -b "$W/php.cj" -o /dev/null -d 'username=admin&password=admin' "$PHP/?_route=admin/post"
curl -fs -c "$W/go.cj" -b "$W/go.cj" -o /dev/null -d 'username=admin&password=admin' "$GO/admin/login"
jar() { awk '(!/^#/ || /^#HttpOnly_/) && NF>=7{printf "%s=%s; ", $6, $7}' "$1"; }
PHP_COOKIE=$(jar "$W/php.cj") GO_COOKIE=$(jar "$W/go.cj")
# grep without -q reads to the end: -q exits at the match, curl gets SIGPIPE and pipefail fails the check
curl -fs -H "Cookie: $PHP_COOKIE" "$PHP/?_route=dashboard" | grep '<title>Dashboard' >/dev/null || { echo "PHPNuxBill login failed" >&2; exit 1; }
curl -fs -H "Cookie: $GO_COOKIE" -o /dev/null -w '%{url_effective}' "$GO/admin/customers" | grep -q customers || { echo "gobill login failed" >&2; exit 1; }

# Warm up opcache, buffer pools and the SQLite page cache.
for u in "$PHP/?_route=dashboard" "$PHP/?_route=customers"; do curl -fso /dev/null -H "Cookie: $PHP_COOKIE" "$u"; done
for u in "$GO/admin" "$GO/admin/customers"; do curl -fso /dev/null -H "Cookie: $GO_COOKIE" "$u"; done
sleep 5
PHP_IDLE=$(pss_php) GO_IDLE=$(pss bench-gobill)

printf 'username=user42&password=pass42' >"$W/auth.form"
declare -A R
log "radius authenticate"
R[php_auth]=$(ab_run -p "$W/auth.form" -T application/x-www-form-urlencoded -H 'X-Freeradius-Section: authenticate' "$PHP/radius.php")
R[go_auth]=$(ab_run -p "$W/auth.form" -T application/x-www-form-urlencoded -H 'X-Freeradius-Section: authenticate' "$GO/radius.php")
log "radius authorize"
R[php_authz]=$(ab_run -p "$W/auth.form" -T application/x-www-form-urlencoded -H 'X-Freeradius-Section: authorize' "$PHP/radius.php")
R[go_authz]=$(ab_run -p "$W/auth.form" -T application/x-www-form-urlencoded -H 'X-Freeradius-Section: authorize' "$GO/radius.php")
log "admin pages"
R[php_list]=$(ab_run -H "Cookie: $PHP_COOKIE" "$PHP/?_route=customers")
R[go_list]=$(ab_run -H "Cookie: $GO_COOKIE" "$GO/admin/customers")
PHP_PEAK=$(peak pss_php ab_run -H "Cookie: $PHP_COOKIE" "$PHP/?_route=dashboard") R[php_dash]=$(cat "$W/out")
GO_PEAK=$(peak "pss bench-gobill" ab_run -H "Cookie: $GO_COOKIE" "$GO/admin") R[go_dash]=$(cat "$W/out")

row() { # name php go
	IFS='|' read -r a1 a2 a3 a4 <<<"${R[$2]}"
	IFS='|' read -r b1 b2 b3 b4 <<<"${R[$3]}"
	awk -v n="$1" -v a1="$a1" -v a2="$a2" -v a3="$a3" -v a4="$a4" -v b1="$b1" -v b2="$b2" -v b3="$b3" -v b4="$b4" \
		'BEGIN{printf "| %s | %s | %s | **%.1fx** | %s / %s ms | %s / %s ms | %s / %s |\n", n, a1, b1, b1/a1, a2, a3, b2, b3, a4, b4}'
}

cat <<EOF

Host: $(lscpu | sed -n 's/^Model name: *//p'), $(nproc) threads, $(free -g | awk '/Mem:/{print $2}') GB RAM, $(uname -sr)
Each server container limited to $CPUS CPUs. $CUSTOMERS customers. ab -k -t $DURATION -c $CONCURRENCY per scenario.

| Scenario | PHPNuxBill req/s | gobill req/s | Speed-up | PHPNuxBill p50 / p99 | gobill p50 / p99 | Non-2xx (PHP / Go) |
|---|---:|---:|---:|---:|---:|---:|
$(row 'RADIUS REST authenticate' php_auth go_auth)
$(row 'RADIUS REST authorize' php_authz go_authz)
$(row 'Admin customer list' php_list go_list)
$(row 'Admin dashboard' php_dash go_dash)

| Resource | PHPNuxBill (Apache + PHP + MariaDB) | gobill |
|---|---:|---:|
| Memory idle, after warm-up (PSS) | $PHP_IDLE MB | $GO_IDLE MB |
| Memory peak under load (PSS) | $PHP_PEAK MB | $GO_PEAK MB |
| Cold start to first HTTP 200 | $PHP_START ms | $GO_START ms |
| Image size | $(podman image inspect -f '{{.Size}}' bench-phpnuxbill docker.io/library/mariadb:10.11 | awk '{s+=$1} END{printf "%.0f MB", s/1e6}') | $(podman image inspect -f '{{.Size}}' bench-gobill | awk '{printf "%.0f MB", $1/1e6}') |
| Import of $CUSTOMERS PHPNuxBill customers | - | $IMPORT_S s |
| Processes | $(($(podman top bench-php-web pid | tail -n +2 | wc -l) + $(podman top bench-php-db pid | tail -n +2 | wc -l))) | $(podman top bench-gobill pid | tail -n +2 | wc -l) |
EOF
