package customers

// Customer status strip: the checks live in internal/service/diagnose, this only gives it the language and the formats.

import (
	"context"
	"fmt"

	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

// Diagnose runs the status checks for one customer.
func (s *Handlers) Diagnose(ctx context.Context, c db.Customer, subs []db.Subscription, online string) diagnose.Diagnosis {
	return (&diagnose.Service{Q: s.Queries, TS: s.TS, Dur: view.HumanDur, Bytes: view.HumanBytes,
		T: func(k string, a ...any) string {
			k = s.T(k)
			if len(a) == 0 {
				return k
			}
			return fmt.Sprintf(k, a...)
		}}).Diagnose(ctx, c, subs, online)
}
