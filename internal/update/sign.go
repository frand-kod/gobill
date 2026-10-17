package update

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SigName is the release file that holds the ed25519 signature of SHA256SUMS, base64 encoded.
// tools/releasesign makes it with the private key that only the maintainer holds.
const SigName = "SHA256SUMS.sig"

// releasePub is the base64 ed25519 public key that release signatures must match. A checksum
// alone only proves the download is intact; the signature proves the maintainer made the release,
// so a leaked GitHub token cannot ship a binary that `gobill update` installs as root.
//
//go:embed release.pub
var releasePub string

// PublicKey returns the built-in release key. A build without one cannot verify releases, so
// `gobill update` refuses to run.
func PublicKey() (ed25519.PublicKey, error) {
	return ParseKey(releasePub)
}

// ParseKey reads a base64 ed25519 public key; lines starting with # are comments.
func ParseKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(stripComments(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("this build has no valid release public key; reinstall from an official release")
	}
	return ed25519.PublicKey(b), nil
}

// VerifySig checks that sig (the contents of SHA256SUMS.sig) is pub's signature of sums.
func VerifySig(sums, sig []byte, pub ed25519.PublicKey) error {
	b, err := base64.StdEncoding.DecodeString(stripComments(string(sig)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return fmt.Errorf("%s is not a valid signature", SigName)
	}
	if !ed25519.Verify(pub, sums, b) {
		return fmt.Errorf("%s does not match SHA256SUMS: the release was not signed by the gobill maintainer", SigName)
	}
	return nil
}

func stripComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}
