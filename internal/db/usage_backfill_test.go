package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A session of two days is split by seconds in each day; a one-day session stays whole; it runs only once.
func TestBackfillUsageDaily(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("WIB", 7*3600)
	at := func(d, h int) int64 { return time.Date(2026, 10, d, h, 0, 0, 0, loc).Unix() }
	ins := "INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at, input_octets, output_octets) VALUES (?, 'alice', '1.1.1.1', ?, ?, ?, ?, ?)"
	// 18:00 on the 12th to 06:00 on the 13th: 6 h in each day
	if _, err := conn.Exec(ins, "a", at(12, 18), at(13, 6), at(13, 6), 1000, 2001); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ins, "b", at(12, 1), at(12, 2), at(12, 2), 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := BackfillUsageDaily(ctx, conn, loc); err != nil {
		t.Fatal(err)
	}
	got := func(day string) (in, out int64) {
		conn.QueryRow("SELECT input_octets, output_octets FROM radius_usage_daily WHERE username='alice' AND day=?", day).Scan(&in, &out)
		return
	}
	if in, out := got("2026-10-12"); in != 510 || out != 1020 { // 500+10, 1000+20
		t.Fatalf("12th: %d/%d", in, out)
	}
	if in, out := got("2026-10-13"); in != 500 || out != 1001 {
		t.Fatalf("13th: %d/%d", in, out)
	}
	if err := BackfillUsageDaily(ctx, conn, loc); err != nil {
		t.Fatal(err)
	}
	if in, _ := got("2026-10-12"); in != 510 {
		t.Fatalf("second run added rows: %d", in)
	}
}
