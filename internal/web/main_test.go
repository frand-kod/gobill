package web

import (
	"os"
	"testing"

	"github.com/frand-kod/gobill/internal/web/core"
	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	core.LoginDelayStep = 0 // the per-username delay would only slow the suite
	os.Exit(m.Run())
}
