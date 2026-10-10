package metrics

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCountersLabelsAndRing(t *testing.T) {
	Reset()
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = time.Now; Reset() })

	Inc("radius_auth_rejected_total", "reason", "expired")
	Add("radius_auth_rejected_total", 2, "reason", "limit")
	Inc("radius_auth_rejected_total", "reason", "expired")
	if v := Value("radius_auth_rejected_total", "reason", "expired"); v != 2 {
		t.Fatalf("expired = %v, want 2", v)
	}
	if v := Recent("radius_auth_rejected_total", 10); v != 4 {
		t.Fatalf("recent = %v, want 4", v)
	}

	// Ten minutes later the old minute is outside a 5-minute window but still in the 24 h series.
	Now = func() time.Time { return at.Add(10 * time.Minute) }
	if v := Recent("radius_auth_rejected_total", 5); v != 0 {
		t.Fatalf("recent 5 = %v, want 0", v)
	}
	if v := Recent("radius_auth_rejected_total", 60); v != 4 {
		t.Fatalf("recent 60 = %v, want 4", v)
	}
	s := Series("radius_auth_rejected_total")
	if len(s) != 1440 || s[1439] != 0 || s[1439-10] != 4 {
		t.Fatalf("series wrong: len %d last %v", len(s), s[1439])
	}

	// A full day later the same ring slot is reused and the old minute is gone.
	Now = func() time.Time { return at.Add(24*time.Hour + time.Minute) }
	Inc("radius_auth_rejected_total")
	if v := Recent("radius_auth_rejected_total", 1440); v != 1 {
		t.Fatalf("after rollover = %v, want 1", v)
	}

	Set("radius_nas_last_packet_timestamp", float64(Now().Unix()), "nas", "10.0.0.1")
	var buf bytes.Buffer
	Expose(&buf, Families())
	out := buf.String()
	for _, want := range []string{
		"# HELP gobill_radius_auth_rejected_total ", "# TYPE gobill_radius_auth_rejected_total counter",
		`gobill_radius_auth_rejected_total{reason="expired"} 2`,
		`# TYPE gobill_radius_nas_last_packet_timestamp gauge`,
		fmt.Sprintf(`gobill_radius_nas_last_packet_timestamp{nas="10.0.0.1"} %g`, float64(Now().Unix())),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition lacks %q:\n%s", want, out)
		}
	}
}

func TestNASTimestampExpiresAndSeriesCapped(t *testing.T) {
	Reset()
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = time.Now; Reset() })

	Set("radius_nas_last_packet_timestamp", float64(at.Unix()), "nas", "10.0.0.1")
	Set("radius_nas_last_packet_timestamp", float64(at.Add(-25*time.Hour).Unix()), "nas", "10.0.0.2")
	Set("radius_nas_last_packet_timestamp", float64(at.Unix()), "nas", "10.0.0.3") // triggers the sweep
	if n := countSeries("radius_nas_last_packet_timestamp"); n != 2 {
		t.Fatalf("series after 24 h sweep = %d, want 2", n)
	}

	// Over the cap the least recently updated series goes first.
	for i := 0; i < maxSeries+10; i++ {
		Now = func() time.Time { return at.Add(time.Duration(i+1) * time.Second) }
		Set("radius_nas_last_packet_timestamp", float64(at.Unix()), "nas", fmt.Sprintf("192.168.%d.%d", i/256, i%256))
	}
	if n := countSeries("radius_nas_last_packet_timestamp"); n != maxSeries {
		t.Fatalf("series after flood = %d, want %d", n, maxSeries)
	}
	if v := Value("radius_nas_last_packet_timestamp", "nas", "10.0.0.1"); v != 0 {
		t.Fatalf("oldest series survived the cap: %v", v)
	}
	Now = func() time.Time { return at.Add(time.Hour) }
	for i := 0; i < maxSeries+5; i++ {
		Inc("radius_auth_rejected_total", "reason", fmt.Sprint("r", i))
	}
	if n := countSeries("radius_auth_rejected_total"); n != maxSeries {
		t.Fatalf("counter series = %d, want %d", n, maxSeries)
	}
}

func countSeries(name string) int {
	n := 0
	Each(name, func([]string, float64) { n++ })
	return n
}

func TestLabelEscape(t *testing.T) {
	if got := Label("k", "a\"b\\c"); got != `k="a\"b\\c"` {
		t.Fatalf("Label = %s", got)
	}
}
