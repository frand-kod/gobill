package web

import (
	"net/http"
	"strings"
	"testing"
)

// The license texts must be served without a login, and must hold every license the binary needs.
func TestLicensesServedWithoutLogin(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(h, "GET", "/licenses", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/licenses: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"GNU GENERAL PUBLIC LICENSE",         // LICENSE (GPL-3.0)
		"Copyright (C) 2026 frand-kod",       // NOTICE
		"Apache License",                     // gozxing (ZXing)
		"Mozilla Public License Version 2.0", // radius, go-sql-driver/mysql
		"Permission is hereby granted",       // MIT texts
		"Alpine.js 3.14.9",                   // web assets
		"ISC License",                        // Lucide icons
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/licenses misses %q", want)
		}
	}
}
