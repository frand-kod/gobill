package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestScrubRemovesURLsTokensAndPhones(t *testing.T) {
	in := "Post https://api.telegram.org/bot123456789:AAHkey_x/sendMessage?text=hi: dial to 081234567890 and +6281234567890 failed"
	got := scrub(in)
	for _, bad := range []string{"https://", "AAHkey", "081234567890", "6281234567890"} {
		if strings.Contains(got, bad) {
			t.Fatalf("scrub kept %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, "<url>") || !strings.Contains(got, "<nomor>") {
		t.Fatalf("scrub = %s", got)
	}
}

func TestRecordKeepsStreakAndLastError(t *testing.T) {
	record("streaktest", errors.New("WA server: HTTP 500: gateway down 0812345678"))
	record("streaktest", errors.New("WA server: HTTP 500"))
	var got ChannelState
	for _, c := range Channels() {
		if c.Name == "streaktest" {
			got = c
		}
	}
	if got.Streak != 2 || strings.Contains(got.LastErr, "0812345678") {
		t.Fatalf("state = %+v", got)
	}
	record("streaktest", nil)
	for _, c := range Channels() {
		if c.Name == "streaktest" && c.Streak != 0 {
			t.Fatalf("streak not reset: %+v", c)
		}
	}
}
