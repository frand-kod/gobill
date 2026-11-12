package chat

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func setup(t *testing.T) (*Service, *db.Queries) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	ctx := context.Background()
	for _, a := range []db.CreateAdminParams{
		{Username: "boss", Role: "SuperAdmin", PasswordHash: "x", Phone: "0811111111"},
		{Username: "rita", Role: "Report", PasswordHash: "x", Phone: "0822222222"},
		{Username: "sales", Role: "Sales", PasswordHash: "x", Phone: "0833333333"},
	} {
		if _, err := q.CreateAdmin(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []db.CreateCustomerParams{
		{Username: "budi", Fullname: "Budi Santoso", Phone: "628123456789", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
		{Username: "ani", Fullname: "Ani Wijaya", Phone: "628555000111", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
		{Username: "twin1", Fullname: "Twin One", Phone: "62777", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
		{Username: "twin2", Fullname: "Twin Two", Phone: "0777", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
	} {
		if _, err := q.CreateCustomer(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Q: q}, q
}

func TestIdentify(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	if c, err := s.Identify(ctx, "0812-3456-789"); err != nil || c.Kind != KindCustomer {
		t.Fatalf("customer by national form: %+v %v", c, err)
	}
	if c, err := s.Identify(ctx, "62811111111"); err != nil || c.Kind != KindAdmin || c.Role != "SuperAdmin" {
		t.Fatalf("admin: %+v %v", c, err)
	}
	if _, err := s.Identify(ctx, "628999"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Identify(ctx, ""); !errors.Is(err, ErrUnknown) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := s.Identify(ctx, "62777"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("shared phone: %v", err)
	}
	if r := s.ReplyUnknown(ErrUnknown); strings.Contains(r, "budi") || r == "" {
		t.Fatalf("unknown reply: %q", r)
	}
}

// A customer sees only their own data, and never an admin tool.
func TestCustomerSeesOwnData(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	budi, _ := s.Identify(ctx, "628123456789")
	if r, _ := s.Reply(ctx, budi, "cek"); !strings.Contains(r, "Budi Santoso") || strings.Contains(r, "Ani") {
		t.Fatalf("cek: %q", r)
	}
	for _, cmd := range []string{"cari ani", "ringkasan", "isolir"} {
		r, tool := s.Reply(ctx, budi, cmd)
		if tool != "" || strings.Contains(r, "Ani") {
			t.Fatalf("customer ran %q: %q (%s)", cmd, r, tool)
		}
	}
	if r, _ := s.Reply(ctx, budi, "bantuan"); strings.Contains(r, "cari") || !strings.Contains(r, "tagihan") {
		t.Fatalf("customer help: %q", r)
	}
	if r, _ := s.Reply(ctx, budi, "bayar"); !strings.Contains(r, "portal") {
		t.Fatalf("bayar: %q", r)
	}
}

// Admin tools are gated by role.
func TestAdminPermissionGate(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	boss, _ := s.Identify(ctx, "0811111111")
	rita, _ := s.Identify(ctx, "0822222222")
	sales, _ := s.Identify(ctx, "0833333333")
	if r, tool := s.Reply(ctx, boss, "cari budi"); tool != "cari" || !strings.Contains(r, "Budi Santoso") {
		t.Fatalf("boss cari: %q", r)
	}
	if _, tool := s.Reply(ctx, rita, "cari budi"); tool != "" {
		t.Fatal("Report role ran cari")
	}
	if r, tool := s.Reply(ctx, rita, "ringkasan"); tool != "ringkasan" || !strings.Contains(r, "Customers: 4") {
		t.Fatalf("Report ringkasan: %q", r)
	}
	for _, cmd := range []string{"cari budi", "ringkasan", "isolir"} {
		if _, tool := s.Reply(ctx, sales, cmd); tool != "" {
			t.Fatalf("Sales ran %s", cmd)
		}
	}
	if r, tool := s.Reply(ctx, sales, "bantuan"); tool != "bantuan" || strings.Contains(r, "cari") {
		t.Fatalf("sales help: %q", r)
	}
}
