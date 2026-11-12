package core

import (
	"context"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
)

// InvoiceData is what the invoice page shows.
type InvoiceData struct {
	T                        db.Transaction
	Customer                 db.Customer
	Company, Address, Footer string
	Portal                   bool // opened by the customer: back link and paid line
}

// Invoice renders one transaction as a printable invoice (admin and portal share it).
func (s *Core) Invoice(w http.ResponseWriter, r *http.Request, t db.Transaction) {
	c := db.Customer{Username: t.Username, Fullname: t.Username} // deleted customer: snapshot only
	if t.CustomerID.Valid {
		var err error
		if c, err = s.Queries.GetCustomer(r.Context(), t.CustomerID.Int64); err != nil {
			s.Fail(w, "invoice customer", err)
			return
		}
	}
	set, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "invoice settings", err)
		return
	}
	s.Render(w, r, 200, "invoice", Page{Title: "Invoice", Data: InvoiceData{t, c, set["company_name"], set["address"], set["note"], strings.HasPrefix(r.URL.Path, "/portal/")}})
}

// IsolirIDs are the isolir plan and its bandwidth, 0 while the plan is not set up.
func (s *Core) IsolirIDs(ctx context.Context) (planID, bwID int64) {
	if planID = isolir.StoredPlanID(ctx, s.Queries); planID == 0 {
		return 0, 0
	}
	if p, err := s.Queries.GetPlan(ctx, planID); err == nil && p.BandwidthID.Valid {
		bwID = p.BandwidthID.Int64
	}
	return planID, bwID
}
