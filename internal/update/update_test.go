package update

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.1.7", "v0.1.6", true},
		{"v0.2.0", "v0.1.9", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.10", "v0.1.9", true}, // numeric, not lexical
		{"v0.1.6", "v0.1.6", false},
		{"v0.1.5", "v0.1.6", false},
		{"v0.1.7", "dev", false},           // dev builds never compare as older or newer
		{"v0.1.7", "v0.1.6-3-gabc", false}, // git describe output is not a release
		{"v0.1.7", "v0.1.6-dirty", false},
		{"v0.1.7", "", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	for _, c := range []struct{ goarch, goarm, want string }{
		{"amd64", "", "gobill-linux-amd64"},
		{"arm64", "", "gobill-linux-arm64"},
		{"arm", "7", "gobill-linux-armv7"},
	} {
		got, err := AssetName(c.goarch, c.goarm)
		if err != nil || got != c.want {
			t.Errorf("AssetName(%q, %q) = %q, %v; want %q", c.goarch, c.goarm, got, err, c.want)
		}
	}
	for _, c := range []struct{ goarch, goarm string }{{"arm", "6"}, {"386", ""}} {
		if got, err := AssetName(c.goarch, c.goarm); err == nil {
			t.Errorf("AssetName(%q, %q) = %q, want an error", c.goarch, c.goarm, got)
		}
	}
}

func TestVerify(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gobill-linux-amd64")
	if err := os.WriteFile(bin, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	// sha256("hello")
	const good = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	sums := []byte(good + "  gobill-linux-amd64\n" + good + "  other\n")
	if err := Verify(bin, sums, "gobill-linux-amd64"); err != nil {
		t.Fatalf("matching checksum refused: %v", err)
	}
	if err := Verify(bin, []byte("0000  gobill-linux-amd64\n"), "gobill-linux-amd64"); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if err := Verify(bin, []byte(good+"  other\n"), "gobill-linux-amd64"); err == nil {
		t.Fatal("missing entry accepted")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			w.Write([]byte(`{"tag_name":"v0.1.7","html_url":"https://x/v0.1.7","body":"notes","assets":[{"name":"SHA256SUMS","browser_download_url":"https://x/s"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	r, err := Fetch(t.Context(), srv.Client(), srv.URL+"/latest")
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "v0.1.7" || r.URL != "https://x/v0.1.7" || r.Notes != "notes" || len(r.Assets) != 1 || r.Assets[0].Name != "SHA256SUMS" {
		t.Fatalf("parsed %+v", r)
	}
	if _, err := Fetch(t.Context(), srv.Client(), srv.URL+"/tags/v9"); err == nil {
		t.Fatal("404 accepted")
	}
}
