package importer

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAllKeepsOrderAndErrors(t *testing.T) {
	pws := []string{"a1", "b2", strings.Repeat("x", 73), "c3"}
	hashes, errs := hashAll(pws)
	for i, pw := range pws {
		if i == 2 {
			if errs[i] == nil {
				t.Fatal("a password over 72 bytes must fail")
			}
			continue
		}
		if errs[i] != nil || bcrypt.CompareHashAndPassword([]byte(hashes[i]), []byte(pw)) != nil {
			t.Fatalf("hash %d does not match %q: %v", i, pw, errs[i])
		}
	}
}
