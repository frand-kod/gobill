package main

import (
	"testing"
	"time"
)

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
