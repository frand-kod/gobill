package radius

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"layeh.com/radius/rfc2866"

	"github.com/frand-kod/gobill/internal/db"
)

// Concurrent accounting writes share commits, every one lands, and a failing write fails only itself.
func TestAccountGroupCommit(t *testing.T) {
	e := setup(t)
	e.s.DB = e.conn
	const n = 300
	bad := errors.New("bad write")
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 7 {
				errs[i] = e.s.commit(t.Context(), func(*db.Queries) error { return bad })
				return
			}
			errs[i] = e.s.Account(t.Context(), AcctRequest{Type: rfc2866.AcctStatusType_Value_Start, NAS: "10.0.0.1",
				SessionID: fmt.Sprint("s", i), User: "alice"})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if (i == 7) != (err != nil) {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	var got int
	e.conn.QueryRow("SELECT count(*) FROM radius_sessions").Scan(&got)
	if got != n-1 {
		t.Fatalf("%d sessions stored, want %d", got, n-1)
	}
}
