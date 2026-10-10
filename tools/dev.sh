#!/bin/sh
# Build and (re)start a local dev instance in the background.
#   tools/dev.sh            build + restart
#   tools/dev.sh stop       stop it
#   tools/dev.sh log        follow its log
# DEV_DIR holds the binary, database, backups and log (default ./dev, gitignored).
# Outbound safety is the database's job: use a copy with notify_customers=no.
set -eu
cd "$(dirname "$0")/.."
DEV_DIR=${DEV_DIR:-./dev}
ADDR=${ADDR:-127.0.0.1:8099}
mkdir -p "$DEV_DIR"
DEV_DIR=$(cd "$DEV_DIR" && pwd)
BIN=$DEV_DIR/gobill-dev

stop() {
	pkill -f "^$BIN" 2>/dev/null || true
	i=0
	while ss -ltn | grep -q " $ADDR "; do
		i=$((i + 1))
		[ $i -gt 10 ] && { echo "port $ADDR still busy (another process?)" >&2; exit 1; }
		sleep 1
	done
}

case "${1:-}" in
stop) stop; echo stopped; exit 0 ;;
log) exec tail -f "$DEV_DIR/app.log" ;;
esac

go build -ldflags "-X main.version=dev-$(git rev-parse --short HEAD)" -o "$BIN.new" ./cmd/gobill
stop
mv "$BIN.new" "$BIN"
GOBILL_DB=$DEV_DIR/dev.db GOBILL_HTTP=$ADDR GOBILL_HTTPS=0 GOBILL_RADIUS=off \
	GOBILL_BACKUP_DIR=$DEV_DIR/backup nohup "$BIN" >>"$DEV_DIR/app.log" 2>&1 &
i=0
until curl -fs -m 2 "http://$ADDR/health" >/dev/null; do
	i=$((i + 1))
	[ $i -gt 15 ] && { echo "did not start, see $DEV_DIR/app.log" >&2; tail -5 "$DEV_DIR/app.log" >&2; exit 1; }
	sleep 1
done
echo "running $(git rev-parse --short HEAD) on http://$ADDR (log: tools/dev.sh log)"
