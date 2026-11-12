package web

import (
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
)

// Parallel wrong guesses must not all pass the cap: only MaxFailedLogins reach the voucher check.
func TestActivationParallelGuessesCapped(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "GOODCODE1", PlanID: p.ID})
	setting(t, e, "disable_registration", "yes")
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(e.h, "POST", "/portal/login/activation", url.Values{"username": {"burst"}, "voucher": {"WRONG"}}, nil)
		}()
	}
	wg.Wait()
	e.s.Mu.Lock()
	n := 0
	for k := range e.s.Failed {
		if strings.HasPrefix(k, "c:") {
			n += len(e.s.Recent(k))
		}
	}
	e.s.Mu.Unlock()
	if n != core.MaxFailedLogins {
		t.Fatalf("counted %d attempts, want %d", n, core.MaxFailedLogins)
	}
	do(e.h, "POST", "/portal/login/activation", url.Values{"username": {"burst"}, "voucher": {"GOODCODE1"}}, nil)
	if _, err := e.q.GetCustomerByUsername(t.Context(), "burst"); err == nil {
		t.Fatal("correct voucher accepted after the cap")
	}
}
