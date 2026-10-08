package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/device"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

// netApp wires a Billing whose routers answer through exec and record every sentence.
func netApp(t *testing.T, exec func([]string) ([]map[string]string, error)) (*Server, *db.Queries, string, func(string, string, url.Values) string, *[]string) {
	s, h, q, c := crudApp(t)
	sent := new([]string)
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey,
		RouterFor: func(db.Router) (device.Router, error) {
			return device.Router{Exec: func(_ context.Context, sn []string) ([]map[string]string, error) {
				*sent = append(*sent, strings.Join(sn, " "))
				return exec(sn)
			}}, nil
		}}
	rt, err := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: "r1", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	// post submits a form, then returns the page the redirect lands on.
	post := func(path, back string, f url.Values) string {
		wantCode(t, do(h, "POST", path, f, c), 303, path)
		return do(h, "GET", back, nil, c).Body.String()
	}
	return s, q, itoa(rt.ID), post, sent
}

func TestRouterPing(t *testing.T) {
	var fail error
	_, _, rid, post, sent := netApp(t, func([]string) ([]map[string]string, error) {
		return []map[string]string{{"name": "Core"}}, fail
	})
	if body := post("/admin/routers/"+rid+"/test", "/admin/routers", nil); !strings.Contains(body, "Core") {
		t.Fatalf("no success flash: %s", body)
	}
	if len(*sent) != 1 || (*sent)[0] != "/system/identity/print" {
		t.Fatalf("sent %v", *sent)
	}
	fail = errors.New("dial tcp: refused")
	if body := post("/admin/routers/"+rid+"/test", "/admin/routers", nil); !strings.Contains(body, "dial tcp: refused") {
		t.Fatalf("no error flash: %s", body)
	}
}

func TestPoolSyncKeepsRowOnFailure(t *testing.T) {
	var fail error
	_, q, rid, post, sent := netApp(t, func([]string) ([]map[string]string, error) { return nil, fail })
	form := url.Values{"name": {"p1"}, "range_ip": {"10.0.0.2-10.0.0.9"}, "router_id": {rid}}
	post("/admin/pool", "/admin/pool", form)
	if len(*sent) != 1 || (*sent)[0] != "/ip/pool/add =name=p1 =ranges=10.0.0.2-10.0.0.9" {
		t.Fatalf("sent %v", *sent)
	}
	fail = errors.New("boom")
	form.Set("name", "p2")
	body := post("/admin/pool/1", "/admin/pool", form)
	if !strings.Contains(body, "boom") {
		t.Fatalf("no warning: %s", body)
	}
	if p, _ := q.GetPool(t.Context(), 1); p.Name != "p2" {
		t.Fatalf("row not kept: %+v", p)
	}
	logs, _ := q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 10})
	found := false
	for _, l := range logs {
		found = found || l.Action == "pool.sync_failed"
	}
	if !found {
		t.Fatal("no sync_failed activity log")
	}
	post("/admin/pool/1/delete", "/admin/pool", nil)
	if _, err := q.GetPool(t.Context(), 1); err == nil {
		t.Fatal("pool not deleted")
	}
}

func TestNASCRUD(t *testing.T) {
	s, h, q, c := crudApp(t)
	form := url.Values{"name": {"edge"}, "ip": {"10.0.0.0/24"}, "secret": {"s3cretvalue-0123456789"}, "description": {"d"}}
	wantCode(t, do(h, "POST", "/admin/nas", form, c), 303, "create")
	list, _ := q.ListNAS(t.Context())
	if len(list) != 1 || strings.Contains(string(list[0].SecretEnc), "s3cretvalue-0123456789") {
		t.Fatalf("nas: %+v", list)
	}
	if plain, err := secret.Open(s.SecretKey, list[0].SecretEnc); err != nil || string(plain) != "s3cretvalue-0123456789" {
		t.Fatalf("open: %q %v", plain, err)
	}
	for _, p := range []string{"/admin/nas", "/admin/nas/1/edit"} {
		if w := do(h, "GET", p, nil, c); w.Code != 200 || strings.Contains(w.Body.String(), "s3cretvalue-0123456789") {
			t.Fatalf("%s: %d or shows secret", p, w.Code)
		}
	}
	// empty secret keeps the old one
	form.Set("secret", "")
	form.Set("ip", "10.0.0.5")
	wantCode(t, do(h, "POST", "/admin/nas/1", form, c), 303, "update")
	n, _ := q.GetNAS(t.Context(), 1)
	if n.Ip != "10.0.0.5" || string(n.SecretEnc) != string(list[0].SecretEnc) {
		t.Fatal("secret not kept")
	}
	// bad CIDR
	form.Set("ip", "10.0.0.0/99")
	w := do(h, "POST", "/admin/nas", form, c)
	wantCode(t, w, 422, "bad cidr")
	if !strings.Contains(w.Body.String(), `id="ip-err"`) {
		t.Fatal("no CIDR error")
	}
	wantCode(t, do(h, "POST", "/admin/nas/1/delete", nil, c), 303, "delete")
}
