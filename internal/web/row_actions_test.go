package web

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Row actions render as icon-only buttons: aria-label for screen readers, data-tip for the hover/focus tooltip.
// Labels are translated (the test app runs in Indonesian), so the check is on the attributes, not the words.
func TestRowActionsAreIconButtons(t *testing.T) {
	e := billApp(t)
	gold := e.plan(t, "gold", "PPPoE", 10000)
	now := time.Now().Unix()
	if _, err := e.q.CreateSubscription(t.Context(), db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: gold.ID,
		RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now, ExpiresAt: now + 86400}); err != nil {
		t.Fatal(err)
	}
	icon := regexp.MustCompile(`<(a|button)[^>]*\bbtn-icon\b[^>]*>`)
	// subscriptions: edit, extend, deactivate, sync (admin sees all four)
	w := do(e.h, "GET", "/admin/subscriptions", nil, e.c)
	if w.Code != 200 {
		t.Fatalf("subscriptions: %d", w.Code)
	}
	checkIconButtons(t, "subscriptions", w.Body.String(), icon, 4)

	// plans: edit and delete
	w = do(e.h, "GET", "/admin/plans", nil, e.c)
	if w.Code != 200 {
		t.Fatalf("plans: %d", w.Code)
	}
	checkIconButtons(t, "plans", w.Body.String(), icon, 2)
	if strings.Contains(w.Body.String(), ">Delete</span>") {
		t.Fatal("delete still renders a text label")
	}
}

func checkIconButtons(t *testing.T, page, body string, icon *regexp.Regexp, min int) {
	t.Helper()
	found := icon.FindAllString(body, -1)
	if len(found) < min {
		t.Fatalf("%s: want >= %d icon buttons, got %d", page, min, len(found))
	}
	for _, el := range found {
		if !strings.Contains(el, `aria-label="`) || !strings.Contains(el, `data-tip="`) {
			t.Fatalf("%s: icon button without aria-label/data-tip: %s", page, el)
		}
	}
}
