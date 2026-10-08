package secret

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRoundTripAndKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	key, created, err := LoadKey("", path)
	if err != nil || !created {
		t.Fatalf("LoadKey: created=%v err=%v", created, err)
	}
	again, created, err := LoadKey("", path)
	if err != nil || created || !bytes.Equal(key, again) {
		t.Fatalf("reload: created=%v err=%v", created, err)
	}
	sealed, err := Seal(key, []byte("pppoe-pass"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, sealed)
	if err != nil || string(got) != "pppoe-pass" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Open(key, sealed); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, _, err := LoadKey("abcd", path); err == nil {
		t.Fatal("short key accepted")
	}
}
