package web

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func promoForm(title string) url.Values {
	return url.Values{"title": {title}, "body": {"Kopi\nGratis ongkir"}, "link": {"https://wa.me/628123456789"},
		"show_isolir": {"1"}, "active": {"1"}}
}

// promoUpload posts the promo form as multipart with an optional image.
func promoUpload(t *testing.T, h http.Handler, c *http.Cookie, path string, fields url.Values, img []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v[0])
	}
	if img != nil {
		fw, _ := mw.CreateFormFile("image", "promo.png")
		fw.Write(img)
	}
	mw.Close()
	r := httptest.NewRequest("POST", path, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPromoAdminCRUDAndRoles(t *testing.T) {
	s, q := newTestApp(t)
	h := s.Handler()
	ctx := t.Context()
	super := login(t, h, "alice")
	mk(t, q, "ada", "Admin", 1)
	admin := login(t, h, "ada")
	report := login(t, h, "rita")

	// Admin and Report roles get 403 on every promo route
	wantCode(t, do(h, "GET", "/admin/settings/promo", nil, admin), 403, "admin list")
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", promoForm("X"), admin), 403, "admin create")
	wantCode(t, do(h, "GET", "/admin/settings/promo", nil, report), 403, "report list")
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", promoForm("X"), report), 403, "report create")

	wantCode(t, do(h, "GET", "/admin/settings/promo", nil, super), 200, "empty list")
	wantCode(t, do(h, "GET", "/admin/settings/promo/new", nil, super), 200, "new form")

	// create two promos; the second one is listed after the first
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", promoForm("Warung Pak Dadi"), super), 303, "create")
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", promoForm("Laundry"), super), 303, "create 2")
	list, _ := q.ListPromos(ctx)
	if len(list) != 2 || list[0].Title != "Warung Pak Dadi" || list[0].Position >= list[1].Position {
		t.Fatalf("created rows %+v", list)
	}
	if list[0].ShowPortal != 0 || list[0].Active != 1 || list[0].ShowIsolir != 1 {
		t.Fatalf("defaults %+v", list[0])
	}

	// reorder: move the first one down, then it is last
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(list[0].ID)+"/down", nil, super), 303, "down")
	list, _ = q.ListPromos(ctx)
	if list[0].Title != "Laundry" || list[1].Title != "Warung Pak Dadi" {
		t.Fatalf("order after down %+v", list)
	}
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(list[1].ID)+"/up", nil, super), 303, "up")
	if l, _ := q.ListPromos(ctx); l[0].Title != "Warung Pak Dadi" {
		t.Fatal("up did not move")
	}

	// edit keeps the id and updates the title
	w := do(h, "GET", "/admin/settings/promo/"+itoa(list[0].ID)+"/edit", nil, super)
	wantCode(t, w, 200, "edit form")
	if !strings.Contains(w.Body.String(), `value="https://wa.me/628123456789"`) {
		t.Fatal("edit form lost the link")
	}
	id := list[0].ID
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(id), promoForm("Warung Dadi"), super), 303, "update")
	if p, _ := q.GetPromo(ctx, id); p.Title != "Warung Dadi" {
		t.Fatalf("update title %q", p.Title)
	}

	// delete with the id; the row goes
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(id)+"/delete", nil, super), 303, "delete")
	if _, err := q.GetPromo(ctx, id); err != sql.ErrNoRows {
		t.Fatalf("delete left the row: %v", err)
	}
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(id)+"/delete", nil, super), 404, "delete twice")
}

func TestPromoValidation(t *testing.T) {
	s, q := newTestApp(t)
	h := s.Handler()
	super := login(t, h, "alice")
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "wa.me/628123", "ftp://x.test/a", "https://"} {
		f := promoForm("Bad")
		f.Set("link", bad)
		wantCode(t, do(h, "POST", "/admin/settings/promo/new", f, super), 422, "link "+bad)
	}
	f := promoForm("")
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", f, super), 422, "empty title")
	f = promoForm("Ranged")
	f.Set("starts", "2026-10-10")
	f.Set("ends", "2026-10-01")
	wantCode(t, do(h, "POST", "/admin/settings/promo/new", f, super), 422, "end before start")
	if list, _ := q.ListPromos(t.Context()); len(list) != 0 {
		t.Fatalf("invalid promos saved: %d", len(list))
	}
	for _, ok := range []string{"https://wa.me/628123", "http://intranet.test/promo"} {
		if !promoLinkOK(ok) {
			t.Fatalf("link refused: %s", ok)
		}
	}
}

// PNG bytes with the signature; DetectContentType only sniffs the header.
var promoPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestPromoImageLifecycle(t *testing.T) {
	s, q := newTestApp(t)
	h := s.Handler()
	super := login(t, h, "alice")
	dir, err := s.uploadDir()
	if err != nil {
		t.Fatal(err)
	}
	// create with an image
	if w := promoUpload(t, h, super, "/admin/settings/promo/new", promoForm("Kopi"), promoPNG); w.Code != 303 {
		t.Fatalf("create with image: %d %s", w.Code, w.Body.String())
	}
	p := mustOnePromo(t, q)
	if !uploadName.MatchString(p.Image) {
		t.Fatalf("image name %q", p.Image)
	}
	file := filepath.Join(dir, p.Image)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("image not stored: %v", err)
	}
	// replacing the image deletes the old file
	if w := promoUpload(t, h, super, "/admin/settings/promo/"+itoa(p.ID), promoForm("Kopi"), nil); w.Code != 303 {
		t.Fatalf("keep image: %d", w.Code)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("image deleted although kept")
	}
	if w := promoUpload(t, h, super, "/admin/settings/promo/"+itoa(p.ID), promoForm("Kopi"), []byte("<svg>x</svg>")); w.Code != 422 {
		t.Fatalf("svg accepted: %d", w.Code)
	}
	if w := promoUpload(t, h, super, "/admin/settings/promo/"+itoa(p.ID), promoForm("Kopi"), promoPNG); w.Code != 303 {
		t.Fatal("replace failed")
	}
	// delete removes the promo and its file
	cur := mustOnePromo(t, q)
	if cur.Image == p.Image {
		t.Fatal("replacement kept the old name")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("replaced image still on disk")
	}
	wantCode(t, do(h, "POST", "/admin/settings/promo/"+itoa(cur.ID)+"/delete", nil, super), 303, "delete")
	if _, err := os.Stat(filepath.Join(dir, cur.Image)); !os.IsNotExist(err) {
		t.Fatal("deleting the promo kept its image")
	}
}

func mustOnePromo(t *testing.T, q *db.Queries) db.Promo {
	t.Helper()
	list, err := q.ListPromos(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("want one promo, got %d (%v)", len(list), err)
	}
	return list[0]
}

func insertPromo(t *testing.T, q *db.Queries, p db.CreatePromoParams) db.Promo {
	t.Helper()
	p.UpdatedAt = time.Now().Unix()
	row, err := q.CreatePromo(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestPromoBlocksRules(t *testing.T) {
	s, q := newTestApp(t)
	ctx := t.Context()
	now := time.Now()
	past := sql.NullInt64{Int64: now.Add(-48 * time.Hour).Unix(), Valid: true}
	future := sql.NullInt64{Int64: now.Add(48 * time.Hour).Unix(), Valid: true}
	insertPromo(t, q, db.CreatePromoParams{Title: "Second", ShowIsolir: 1, Active: 1, Position: 2})
	insertPromo(t, q, db.CreatePromoParams{Title: "Inactive", ShowIsolir: 1, Active: 0, Position: 0})
	insertPromo(t, q, db.CreatePromoParams{Title: "Ended", ShowIsolir: 1, Active: 1, EndsAt: past, Position: 0})
	insertPromo(t, q, db.CreatePromoParams{Title: "Later", ShowIsolir: 1, Active: 1, StartsAt: future, Position: 0})
	insertPromo(t, q, db.CreatePromoParams{Title: "Portal only", ShowIsolir: 0, ShowPortal: 1, Active: 1, Position: 0})
	insertPromo(t, q, db.CreatePromoParams{Title: "<script>x</script>", Body: "<b>hi</b>", Link: "https://wa.me/1", Image: "0123456789abcdef0123456789abcdef.png", ShowIsolir: 1, Active: 1, Position: 1})

	html := string(s.promoBlocks(ctx, "isolir"))
	for _, hidden := range []string{"Inactive", "Ended", "Later", "Portal only"} {
		if strings.Contains(html, hidden) {
			t.Fatalf("%q shown on isolir", hidden)
		}
	}
	if strings.Contains(html, "<script>x</script>") || !strings.Contains(html, "&lt;script&gt;x&lt;/script&gt;") ||
		!strings.Contains(html, "&lt;b&gt;hi&lt;/b&gt;") {
		t.Fatalf("not escaped: %s", html)
	}
	if strings.Index(html, "&lt;script") > strings.Index(html, "Second") {
		t.Fatal("position order lost")
	}
	if !strings.Contains(html, `target="_blank" rel="noopener"`) || !strings.Contains(html, `loading="lazy"`) {
		t.Fatal("link or lazy image attributes missing")
	}
	if got := s.promoBlocks(ctx, "portal"); !strings.Contains(string(got), "Portal only") {
		t.Fatal("portal place missing its promo")
	}
	if s.promoBlocks(ctx, "nobody") != "" {
		t.Fatal("unknown place rendered something")
	}
}

func TestPortalPromoCard(t *testing.T) {
	e := billApp(t)
	q := e.q
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")

	// an isolir-only promo never shows in the bell
	insertPromo(t, q, db.CreatePromoParams{Title: "Isolir only", ShowIsolir: 1, Active: 1})
	if body := portalGet(t, e, c, "/portal"); strings.Contains(body, "Isolir only") {
		t.Fatal("isolir promo shown in the portal")
	}

	insertPromo(t, q, db.CreatePromoParams{Title: "Kopi Dadi", Body: "Diantar", ShowPortal: 1, Active: 1, Position: 1})
	insertPromo(t, q, db.CreatePromoParams{Title: "Laundry Kilat", ShowPortal: 1, Active: 1, Position: 2})
	if body := portalGet(t, e, c, "/portal"); !strings.Contains(body, "Kopi Dadi") || strings.Contains(body, "gb-promo-") {
		t.Fatal("first portal promo missing from the bell, or the old card is back")
	}
}
