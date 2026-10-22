package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/frand-kod/gobill/internal/update"
)

// runVerifyRelease checks SHA256SUMS.sig against SHA256SUMS with the built-in release key.
// deploy/install.sh calls it on a fresh download; it is also the manual check.
func runVerifyRelease(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: gobill verify-release <SHA256SUMS> <SHA256SUMS.sig>")
	}
	pub, err := update.PublicKey()
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	if err := update.VerifySig(sums, sig, pub); err != nil {
		return err
	}
	fmt.Println("signature ok")
	return nil
}
