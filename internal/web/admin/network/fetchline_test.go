package network

import (
	"strings"
	"testing"
)

func TestFetchLineHTTPSChecksCertificate(t *testing.T) {
	if l := fetchLine("https://gobill.example/provision/abc"); !strings.Contains(l, "mode=https") || !strings.Contains(l, "check-certificate=yes") {
		t.Fatalf("https line: %s", l)
	}
	if l := fetchLine("http://10.0.0.1:8080/provision/abc"); strings.Contains(l, "check-certificate") {
		t.Fatalf("http line must not demand a certificate: %s", l)
	}
}
