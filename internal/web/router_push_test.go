package web

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestRouterPushAll(t *testing.T) {
	var fail error
	_, q, rid, post, sent := netApp(t, func([]string) ([]map[string]string, error) {
		return []map[string]string{{"name": "Core"}}, fail
	})
	push := func() string { return post("/admin/routers/"+rid+"/sync", "/admin/routers", nil) }
	if body := push(); !strings.Contains(body, "Dorong semua selesai") || !strings.Contains(body, "0 berhasil, 0 gagal") {
		t.Fatalf("no summary: %s", body)
	}
	// offline: one ping, nothing else sent
	fail = errors.New("dial tcp: refused")
	*sent = nil
	if body := push(); !strings.Contains(body, "Dorong semua gagal") || !strings.Contains(body, "router offline") || len(*sent) != 1 {
		t.Fatalf("offline: %d sent, %s", len(*sent), body)
	}
	// a router that only has RADIUS plans is refused
	fail = nil
	bw, _ := q.CreateBandwidth(t.Context(), db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	rt, _ := q.GetRouter(t.Context(), 1)
	_, err := q.CreatePlan(t.Context(), db.CreatePlanParams{Name: "rp", Type: "PPPoE", Billing: "prepaid", Price: 1, Validity: 1, ValidityUnit: "Days",
		Enabled: 1, Device: "Radius", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if body := push(); !strings.Contains(body, "mode RADIUS") {
		t.Fatalf("radius not refused: %s", body)
	}
}
