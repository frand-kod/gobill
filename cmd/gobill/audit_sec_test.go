package main

import (
	"os"
	"testing"
)

// #2: a signed SHA256SUMS from an old release, republished under a newer tag, must be refused:
// the signature has to bind the version, or a stolen GitHub token can downgrade to a vulnerable build.
func TestAuditSec2_SignatureDoesNotBindVersion(t *testing.T) {
	old := []byte("old-signed-vulnerable-build")
	srv := fakeGitHub(t, "v9.9.9", old, sumsFor(old), true) // sums carry no version line, as real ones
	tu := newTestUpdater(t, srv.URL, "")
	err := tu.update(t.Context(), "v0.2.4", "", true)
	if b, _ := os.ReadFile(tu.bin); err == nil && string(b) == string(old) {
		t.Fatal("installed a binary whose signed SHA256SUMS does not name the release tag")
	}
}
