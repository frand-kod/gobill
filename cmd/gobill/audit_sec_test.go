package main

import (
	"os"
	"strings"
	"testing"
)

// #2: the signature binds the version. A signed SHA256SUMS from an old release republished under
// a newer tag, or one without a VERSION line, must be refused.
func TestAuditSec2_SignatureDoesNotBindVersion(t *testing.T) {
	old := []byte("old-signed-vulnerable-build")
	cases := map[string]struct{ sums, want string }{
		"no version line":  {sumsFor(old), "no signed VERSION"},
		"older signed tag": {sumsFor(old) + "VERSION v0.1.0\n", "does not match"},
		"matching tag":     {sumsFor(old) + "VERSION v9.9.9\n", ""}, // control: accepted
	}
	for name, c := range cases {
		srv := fakeGitHubRaw(t, "v9.9.9", old, c.sums, true)
		tu := newTestUpdater(t, srv.URL, "")
		err := tu.update(t.Context(), "v0.2.4", "", true)
		if c.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err %v, want %q", name, err, c.want)
		}
		if b, _ := os.ReadFile(tu.bin); string(b) != "old" {
			t.Fatalf("%s: binary replaced", name)
		}
	}
}

func TestAuditSec2_ExplicitVersionMayRollBack(t *testing.T) {
	bin := []byte("b")
	srv := fakeGitHub(t, "v0.2.0", bin, sumsFor(bin), true)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.2.4", "v0.2.0", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tu.bin); string(b) != "b" {
		t.Fatal("explicit --version did not install")
	}
}
