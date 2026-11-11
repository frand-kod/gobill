package clock

import (
	"testing"
	"time"
)

func TestFunc(t *testing.T) {
	at := time.Unix(100, 0)
	if got := Func(func() time.Time { return at }).Now(); !got.Equal(at) {
		t.Fatal(got)
	}
	var f Func
	if f.Now().IsZero() {
		t.Fatal("nil Func must use the real clock")
	}
	if _, off := time.Date(2025, 1, 1, 0, 0, 0, 0, Jakarta()).Zone(); off != 7*3600 {
		t.Fatal(off)
	}
}
