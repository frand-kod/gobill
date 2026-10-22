package web

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	loginDelayStep = 0 // the per-username delay would only slow the suite
	os.Exit(m.Run())
}
