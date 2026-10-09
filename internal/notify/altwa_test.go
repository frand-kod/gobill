package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAltWARequest(t *testing.T) {
	var path, ct, dev, user, pass, got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, ct, dev, got = r.Method+" "+r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("X-Device-Id"), string(b)
		user, pass, _ = r.BasicAuth()
		w.Write([]byte(`{"code":"SUCCESS","message":"ok"}`))
	}))
	defer srv.Close()
	n := nt(map[string]string{"alt_wga_server_url": srv.URL + "/", "alt_wga_device_id": "dev1", "alt_wga_username": "u", "alt_wga_password": "p", "country_code_phone": "62"})
	if err := n.WhatsApp(context.Background(), "0812-3456 789", "halo"); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	json.Unmarshal([]byte(got), &body)
	if path != "POST /send/message" || !strings.HasPrefix(ct, "application/json") || dev != "dev1" || user != "u" || pass != "p" ||
		body["phone"] != "628123456789@s.whatsapp.net" || body["message"] != "halo" {
		t.Fatalf("bad request: %s %s %s %s:%s %v", path, ct, dev, user, pass, body)
	}
}

func TestAltWAErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{{401, `{"code":"401","message":"bad credentials"}`, "bad credentials"}, {200, `{"code":"400","message":"not logged in"}`, "not logged in"}, {500, `boom`, "boom"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		err := nt(map[string]string{"alt_wga_server_url": srv.URL, "alt_wga_password": "SECRETPW"}).WhatsApp(context.Background(), "628123456789", "x")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "SECRETPW") {
			t.Fatalf("%d: %v", c.status, err)
		}
	}
	if err := nt(map[string]string{"alt_wga_server_url": "http://x"}).AltWA(context.Background(), "12", "x"); err == nil {
		t.Fatal("short number accepted")
	}
}

func TestAltWASecretsNotLogged(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	var logged error
	n := nt(map[string]string{"alt_wga_server_url": dead + "/?key=URLKEY", "alt_wga_password": "SECRETPW", "alt_wga_username": "u"})
	n.Log = func(_, _, _, _ string, err error) { logged = err }
	err := n.WhatsApp(context.Background(), "628123456789", "OTP 654321")
	if err == nil || logged == nil {
		t.Fatal("want error")
	}
	for _, bad := range []string{"SECRETPW", "URLKEY", "654321"} {
		if strings.Contains(logged.Error(), bad) {
			t.Fatalf("%q leaked: %v", bad, logged)
		}
	}
}

func TestWAFallbackAndPHPPluginIgnored(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.Write([]byte(`{"code":"SUCCESS"}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	// no alt_wga: the wa_url template is used
	if err := nt(map[string]string{"wa_url": srv.URL + "/old?n=[number]&t=[text]"}).WhatsApp(ctx, "628123456789", "x"); err != nil {
		t.Fatal(err)
	}
	// alt_wga set and wa_url is the PHP plugin route: wa_url is not called
	php := srv.URL + "/?_route=plugin/wga_sendMessage&phone=[[phone]]&message=[[text]]"
	if err := nt(map[string]string{"wa_url": php, "alt_wga_server_url": srv.URL}).WhatsApp(ctx, "628123456789", "x"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(hits, ",") != "/old,/send/message" {
		t.Fatalf("hits %v", hits)
	}
	if !WAConfigured(map[string]string{"alt_wga_server_url": "http://x"}) || WAConfigured(map[string]string{}) {
		t.Fatal("WAConfigured")
	}
}
