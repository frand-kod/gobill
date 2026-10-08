package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestCoordsValidation(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		" -6.2 , 106.8 ":   "-6.2,106.8",
		"-90,180":          "-90,180",
		"-6.200000,106.81": "-6.2,106.81",
	} {
		if got, ok := checkCoords(in); !ok || got != want {
			t.Errorf("checkCoords(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"-6.2", "91,0", "0,181", "a,b", "NaN,1", "1,2,3", "1;2"} {
		if _, ok := checkCoords(in); ok {
			t.Errorf("checkCoords(%q) accepted", in)
		}
	}
}

func TestODPCRUD(t *testing.T) {
	_, h, q, c := crudApp(t)
	form := url.Values{"name": {"ODP-A"}, "coordinates": {"-6.2,106.8"}, "port_amount": {"8"}, "attenuation": {"-20dB"}, "coverage": {"300"}}

	bad := url.Values{"name": {"ODP-B"}, "coordinates": {"200,1"}, "port_amount": {"8"}}
	w := do(h, "POST", "/admin/odp", bad, c)
	wantCode(t, w, 422, "bad coordinates")
	if !strings.Contains(w.Body.String(), `aria-invalid="true"`) {
		t.Fatal("coordinate error not shown")
	}

	wantCode(t, do(h, "POST", "/admin/odp", form, c), 303, "create")
	list, _ := q.SearchODPs(t.Context(), db.SearchODPsParams{PageLimit: 10})
	if len(list) != 1 || list[0].Coordinates != "-6.2,106.8" || list[0].Coverage != 300 {
		t.Fatalf("odps: %+v", list)
	}
	wantCode(t, do(h, "POST", "/admin/odp", form, c), 422, "duplicate name")

	id := "/admin/odp/" + itoa(list[0].ID)
	form.Set("port_amount", "16")
	form.Set("router_id", "999")
	wantCode(t, do(h, "POST", id, form, c), 422, "unknown router")
	form.Del("router_id")
	wantCode(t, do(h, "POST", id, form, c), 303, "update")
	if o, _ := q.GetODP(t.Context(), list[0].ID); o.PortAmount != 16 {
		t.Fatal("not updated")
	}
	if w := do(h, "GET", id+"/edit", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), `value="-6.2,106.8"`) {
		t.Fatal("edit form")
	}
	wantCode(t, do(h, "POST", id+"/delete", nil, c), 303, "delete")
	if list, _ := q.SearchODPs(t.Context(), db.SearchODPsParams{PageLimit: 10}); len(list) != 0 {
		t.Fatal("not deleted")
	}
}

// Map endpoints list only rows that have coordinates.
func TestMapDataOnlyWithCoordinates(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx := t.Context()
	q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "geo", PasswordHash: "x", Fullname: "Geo", ServiceType: "Others", Status: "Active", Coordinates: "-6.2,106.8"})
	q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "nogeo", PasswordHash: "x", Fullname: "NoGeo", ServiceType: "Others", Status: "Active"})
	q.CreateRouter(ctx, db.CreateRouterParams{Name: "R-geo", Host: "10.0.0.1", Port: 8728, Username: "api", PasswordEnc: []byte("x"), Enabled: 1, Coordinates: "-7,110", Coverage: 500})
	q.CreateRouter(ctx, db.CreateRouterParams{Name: "R-nogeo", Host: "10.0.0.2", Port: 8728, Username: "api", PasswordEnc: []byte("x"), Enabled: 1})
	wantCode(t, do(h, "POST", "/admin/odp", url.Values{"name": {"ODP-geo"}, "coordinates": {"-7.1,110.2"}, "coverage": {"100"}}, c), 303, "odp")
	wantCode(t, do(h, "POST", "/admin/odp", url.Values{"name": {"ODP-nogeo"}}, c), 303, "odp without coordinates")

	cases := map[string]struct {
		path, title string
		radius      bool
	}{
		"/admin/maps/customers/data": {title: "Geo"},
		"/admin/maps/routers/data":   {title: "R-geo", radius: true},
		"/admin/maps/odp/data":       {title: "ODP-geo", radius: true},
	}
	for path, want := range cases {
		w := do(h, "GET", path, nil, c)
		if w.Code != 200 {
			t.Fatalf("%s: got %d", path, w.Code)
		}
		var body struct {
			Markers []mapMarker `json:"markers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Markers) != 1 || body.Markers[0].Title != want.title {
			t.Fatalf("%s: markers %+v", path, body.Markers)
		}
		if (body.Markers[0].Radius > 0) != want.radius {
			t.Fatalf("%s: radius %d", path, body.Markers[0].Radius)
		}
	}
	if w := do(h, "GET", "/admin/maps/customers", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), `data-src="/admin/maps/customers/data"`) {
		t.Fatal("customer map page")
	}
}

func TestReportRoleCannotPostODP(t *testing.T) {
	_, h, _, _ := crudApp(t)
	rc := login(t, h, "rita")
	form := url.Values{"name": {"X"}}
	for _, p := range []string{"/admin/odp", "/admin/odp/1", "/admin/odp/1/delete"} {
		if w := do(h, "POST", p, form, rc); w.Code != http.StatusForbidden {
			t.Fatalf("POST %s: got %d", p, w.Code)
		}
	}
	for _, p := range []string{"/admin/odp", "/admin/maps/odp", "/admin/maps/routers"} {
		if w := do(h, "GET", p, nil, rc); w.Code != http.StatusForbidden {
			t.Fatalf("GET %s: got %d", p, w.Code)
		}
	}
}
