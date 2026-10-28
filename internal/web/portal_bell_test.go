package web

import (
	"net/http/httptest"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestPortalBellUnreadAndSeen(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	a, _ := custLogin(t, e, "u1", "pw12345")
	setAnn := func(body string) {
		if err := e.q.UpdatePageBody(t.Context(), db.UpdatePageBodyParams{Body: body, Slug: "announcement"}); err != nil {
			t.Fatal(err)
		}
	}
	unread := func() int {
		items, err := e.s.portalBell(httptest.NewRequest("GET", "/portal", nil).WithContext(t.Context()), &e.cust)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, it := range items {
			if it.Unread {
				n++
			}
		}
		return n
	}
	setAnn("Maintenance tonight")
	if n := unread(); n != 1 {
		t.Fatalf("unread = %d, want 1", n)
	}
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", nil, a), 204, "mark seen")
	if n := unread(); n != 0 {
		t.Fatalf("unread after seen = %d", n)
	}
	setAnn("Maintenance moved to Friday")
	if n := unread(); n != 1 {
		t.Fatalf("unread after change = %d, want 1", n)
	}
}
