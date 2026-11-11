package setup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/settings"
)

func TestSteps(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	saved := 0
	s := &Service{Q: q, PlanSvc: &plan.Service{DB: conn, Q: q}, Langs: []string{"en"}, Saved: func(context.Context) { saved++ }}
	ctx := context.Background()

	if e, err := s.QRIS(ctx, "", "not a qris"); err != nil || e["qris_payload"] == "" || saved != 0 {
		t.Fatalf("qris: %v %v", e, err)
	}
	if err := s.Isolir(ctx, true); err != nil || settings.Get(ctx, q, "isolir_enabled") != "yes" || settings.Get(ctx, q, "isolir_plan_id") == "" {
		t.Fatalf("isolir on: %v", err)
	}
	if err := s.Isolir(ctx, false); err != nil || settings.Get(ctx, q, "isolir_enabled") != "no" {
		t.Fatalf("isolir off: %v", err)
	}
	// a priced template without a bandwidth is refused on the wizard's field, nothing is created
	created, errs, err := s.Plans(ctx, PlansInput{Templates: []PlanTemplate{{Key: "price_0", Name: "Daily", Price: "5000", Validity: 1, Unit: "Days"}}})
	if err != nil || len(created) != 0 || errs["plan_bandwidth"] == "" {
		t.Fatalf("plans: %v %v %v", created, errs, err)
	}
}
