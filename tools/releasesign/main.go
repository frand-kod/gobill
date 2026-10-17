// Command releasesign makes and checks the ed25519 signature of a release's SHA256SUMS.
//
//	go run ./tools/releasesign keygen <private-key-file>   write a new key pair, print the public key
//	go run ./tools/releasesign sign <private-key-file> <SHA256SUMS>   write SHA256SUMS.sig next to it
//	go run ./tools/releasesign verify <SHA256SUMS>   check SHA256SUMS.sig against internal/update/release.pub
//	go run ./tools/releasesign check-key   fail unless internal/update/release.pub holds a valid key (CI)
//
// The private key never goes to GitHub: keep it, and a backup, offline. See SECURITY.md.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frand-kod/gobill/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		return keygen(args[1])
	case len(args) == 3 && args[0] == "sign":
		return sign(args[1], args[2])
	case len(args) == 2 && args[0] == "verify":
		return verify(args[1])
	case len(args) == 1 && args[0] == "check-key":
		_, err := update.PublicKey()
		return err
	}
	return errors.New("usage: releasesign keygen <key> | sign <key> <SHA256SUMS> | verify <SHA256SUMS> | check-key")
}

func keygen(path string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// O_EXCL: never overwrite a key that already signed releases.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("private key: %s (keep it offline, back it up)\n", path)
	fmt.Println("public key, put it in internal/update/release.pub:")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(keyPath, sumsPath string) error {
	k, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(k)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%s is not a releasesign private key", keyPath)
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	pub, err := update.PublicKey()
	if err != nil {
		return err
	}
	if !ed25519.PrivateKey(priv).Public().(ed25519.PublicKey).Equal(pub) {
		return errors.New("this private key does not match internal/update/release.pub")
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n"
	out := filepath.Join(filepath.Dir(sumsPath), update.SigName)
	if err := os.WriteFile(out, []byte(sig), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", out)
	return nil
}

func verify(sumsPath string) error {
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(filepath.Dir(sumsPath), update.SigName))
	if err != nil {
		return err
	}
	pub, err := update.PublicKey()
	if err != nil {
		return err
	}
	if err := update.VerifySig(sums, sig, pub); err != nil {
		return err
	}
	fmt.Println("signature OK")
	return nil
}
