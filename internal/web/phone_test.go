package web

import (
	"slices"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// The same number typed in three forms is one number: uniqueness (wa_unique_per_account = yes) and
// the forgot-username lookup both see it, whatever form was stored or typed.
func TestPhoneSameNumberAcrossFormats(t *testing.T) {
	s, q := newTestApp(t)
	ctx := t.Context()
	budi, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "budi", PasswordHash: "h", Fullname: "Budi",
		Phone: "0812-3456-789", ServiceType: "Others", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "wa_unique_per_account", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	for _, typed := range []string{"0812-3456-789", "+62 812 3456 789", "628123456789"} {
		taken, err := s.phoneTaken(ctx, typed, 0)
		if err != nil || !taken {
			t.Errorf("phoneTaken(%q) = %v, %v; want taken", typed, taken, err)
		}
		if taken, _ := s.phoneTaken(ctx, typed, budi.ID); taken {
			t.Errorf("phoneTaken(%q) for the owner itself must not be taken", typed)
		}
		names, err := s.usernamesFor(ctx, typed, false)
		if err != nil || !slices.Equal(names, []string{"budi"}) {
			t.Errorf("usernamesFor(%q) = %v, %v; want [budi]", typed, names, err)
		}
	}
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "wa_unique_per_account", Value: "no"}); err != nil {
		t.Fatal(err)
	}
	if taken, _ := s.phoneTaken(ctx, "+62 812 3456 789", 0); taken {
		t.Error("wa_unique_per_account = no: a shared number is allowed")
	}
}
