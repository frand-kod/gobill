package main

import (
	"os"
	"testing"
	"time"
)

func TestRadiusAddr(t *testing.T) {
	t.Setenv("NUXBILL_RADIUS", "x")
	os.Unsetenv("NUXBILL_RADIUS")
	if a, on := radiusAddr(); a != ":1812" || !on {
		t.Errorf("unset: got %q, %v", a, on)
	}
	t.Setenv("NUXBILL_RADIUS", "")
	if a, on := radiusAddr(); on {
		t.Errorf("empty: got %q, enabled", a)
	}
	t.Setenv("NUXBILL_RADIUS", "off")
	if a, on := radiusAddr(); on {
		t.Errorf("off: got %q, enabled", a)
	}
	t.Setenv("NUXBILL_RADIUS", ":11812")
	if a, on := radiusAddr(); a != ":11812" || !on {
		t.Errorf("custom: got %q, %v", a, on)
	}
}

func TestLoadZone(t *testing.T) {
	def := time.FixedZone("def", 0)
	jkt, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	if got := loadZone("", jkt); got != jkt {
		t.Errorf("empty name: got %v, want Asia/Jakarta", got)
	}
	if got := loadZone("   ", jkt); got != jkt {
		t.Errorf("blank name: got %v, want Asia/Jakarta", got)
	}
	if got := loadZone("Bogus/Zone", def); got != def {
		t.Errorf("unknown name: got %v, want fallback", got)
	}
	if got := loadZone("Asia/Tokyo", jkt); got.String() != "Asia/Tokyo" {
		t.Errorf("valid name: got %v", got)
	}
}
