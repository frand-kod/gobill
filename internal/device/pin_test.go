package device

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// tlsServer is a TLS listener with a self-signed cert; the RouterOS login simply fails after the handshake.
func tlsServer(t *testing.T) (addr, fp string) {
	t.Helper()
	// httptest's built-in cert is the same for every server, so each gets its own key
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), CertFingerprint(srv.Certificate())
}

func TestCertPin(t *testing.T) {
	ctx := context.Background()
	sent := []string{"/system/identity/print"}
	addr, fp := tlsServer(t)
	var pinned string
	r := Router{Addr: addr, TLS: true, OnPin: func(f string) { pinned = f }}
	r.exec(ctx, sent)
	if pinned != fp {
		t.Fatalf("first connect pinned %q, want %q", pinned, fp)
	}

	// matching pin: accepted, nothing re-recorded
	pinned = ""
	r.Pin = fp
	_, err := r.exec(ctx, sent)
	if pinned != "" || (err != nil && strings.Contains(err.Error(), "pinned")) {
		t.Fatalf("matching pin rejected or re-pinned: %v %q", err, pinned)
	}

	// a different certificate is refused and both fingerprints are named
	other, otherFP := tlsServer(t)
	r2 := Router{Addr: other, TLS: true, Pin: fp}
	_, err = r2.exec(ctx, sent)
	if err == nil || !strings.Contains(err.Error(), fp) || !strings.Contains(err.Error(), otherFP) {
		t.Fatalf("mismatch not refused with both fingerprints: %v", err)
	}

	// reset (empty pin) pins the new certificate
	r2.Pin = ""
	r2.OnPin = func(f string) { pinned = f }
	r2.exec(ctx, sent)
	if pinned != otherFP {
		t.Fatalf("after reset pinned %q, want %q", pinned, otherFP)
	}
}
