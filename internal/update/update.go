// Package update reads gobill releases from GitHub and compares versions. The `gobill update`
// command and the in-app update check both use it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// APIURL is the GitHub releases endpoint. Fetch takes APIURL+"/latest" or APIURL+"/tags/<tag>".
const APIURL = "https://api.github.com/repos/frand-kod/gobill/releases"

// Page is the releases page shown to operators.
const Page = "https://github.com/frand-kod/gobill/releases"

// Release is the part of a GitHub release that gobill uses.
type Release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Notes  string  `json:"body"`
	Assets []Asset `json:"assets"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Fetch reads one release from the GitHub API.
func Fetch(ctx context.Context, client *http.Client, url string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("github: %w", err)
	}
	if r.Tag == "" {
		return Release{}, errors.New("github: release has no tag_name")
	}
	return r, nil
}

// Parse reads a release version vMAJOR.MINOR.PATCH. ok is false for anything else, such as "dev"
// or git describe output after a tag ("v0.1.6-3-gabc").
func Parse(v string) (ver [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return ver, false
	}
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return ver, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return ver, false
		}
		ver[i] = n
	}
	return ver, true
}

// Newer reports whether tag a is a higher release than b. If either is not a release version
// (a dev build), it reports false.
func Newer(a, b string) bool {
	x, okA := Parse(a)
	y, okB := Parse(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// AssetName is the release file for linux/goarch. goarm is the GOARM the running binary was built
// with; only 7 has a release build.
func AssetName(goarch, goarm string) (string, error) {
	switch {
	case goarch == "amd64":
		return "gobill-linux-amd64", nil
	case goarch == "arm64":
		return "gobill-linux-arm64", nil
	case goarch == "arm" && goarm == "7":
		return "gobill-linux-armv7", nil
	}
	return "", fmt.Errorf("no release build for linux/%s (GOARM=%q)", goarch, goarm)
}

// Verify checks that the file at path has the SHA-256 that sums (a SHA256SUMS file in sha256sum
// format) lists for name. A missing entry is an error: an unverified binary is never installed.
func Verify(path string, sums []byte, name string) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, SHA256SUMS says %s", name, got, want)
	}
	return nil
}
