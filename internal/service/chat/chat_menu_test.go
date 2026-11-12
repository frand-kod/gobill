package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// An admin whose phone also belongs to one customer gets admin tools first, then their own customer tools.
func TestAdminOwnCustomerFallback(t *testing.T) {
	s, q := setup(t)
	ctx := context.Background()
	if _, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "bossnet", Fullname: "Boss Net", Phone: "0811111111",
		ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Identify(ctx, "0811111111")
	if err != nil || c.Kind != KindAdmin || c.OwnCustomerID == 0 {
		t.Fatalf("identify = %+v, %v", c, err)
	}
	if out, tool := s.Reply(ctx, c, "cari budi"); tool != "cari" || !strings.Contains(out, "budi") {
		t.Fatalf("cari = %q %q", tool, out)
	}
	if out, tool := s.Reply(ctx, c, "/cek"); tool != "cek" || !strings.Contains(out, "Boss Net") || strings.Contains(out, "Budi") {
		t.Fatalf("own cek = %q %q", tool, out)
	}
	if _, tool := s.Reply(ctx, c, "1"); tool != "cek" {
		t.Fatalf("menu digit for admin's own account = %q", tool)
	}
	if out, _ := s.Reply(ctx, c, "bantuan"); !strings.Contains(out, "cari") || !strings.Contains(out, "1. ") {
		t.Fatalf("admin bantuan lacks admin or own menu: %q", out)
	}

	// an admin without a customer account gets no customer tools
	sales, _ := s.Identify(ctx, "0833333333")
	if sales.OwnCustomerID != 0 {
		t.Fatal("sales has no customer account")
	}
	if _, tool := s.Reply(ctx, sales, "cek"); tool != "" {
		t.Fatalf("admin without customer account ran %q", tool)
	}
}

func TestCustomerMenuAndSlash(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	c, _ := s.Identify(ctx, "628123456789")
	if out, _ := s.Reply(ctx, c, "menu"); !strings.Contains(out, "1. ") || !strings.Contains(out, "(gangguan)") {
		t.Fatalf("menu = %q", out)
	}
	for in, want := range map[string]string{"1": "cek", "/cek": "cek", "CEK": "cek", "6": "paket", "9": "", "12": ""} {
		if _, tool := s.Reply(ctx, c, in); tool != want {
			t.Errorf("%q ran %q, want %q", in, tool, want)
		}
	}
}
