package public

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/core"
)

const waSecret = "0123456789abcdef-secret"

type waRig struct {
	h    *Handlers
	q    *db.Queries
	mu   sync.Mutex
	sent map[string][]string // phone -> replies
}

func newWARig(t *testing.T, on bool) *waRig {
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
	if on {
		for k, v := range map[string]string{"alt_wga_webhook_secret": waSecret, "wa_inbound": "yes"} {
			if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []db.CreateCustomerParams{
		{Username: "budi", Fullname: "Budi Santoso", Phone: "628123456789", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
		{Username: "ani", Fullname: "Ani Wijaya", Phone: "628555000111", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"},
	} {
		if _, err := q.CreateCustomer(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	c := &core.Core{Queries: q, Settings: settings.New(q), Failed: map[string][]time.Time{}}
	c.Lang.Store("english")
	r := &waRig{h: New(c), q: q, sent: map[string][]string{}}
	r.h.waSend = func(_ context.Context, phone, text string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.sent[phone] = append(r.sent[phone], text)
		return nil
	}
	return r
}

func waBody(id, from, body string) string {
	return `{"event":"message","device_id":"628@s.whatsapp.net","payload":{"id":"` + id + `","chat_id":"` + from + `","from":"` + from + `","is_from_me":false,"body":"` + body + `"}}`
}

func (r *waRig) post(body, sig string) int {
	req := httptest.NewRequest("POST", "/wa/inbound", strings.NewReader(body))
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	w := httptest.NewRecorder()
	r.h.waInbound(w, req)
	r.h.waWG.Wait()
	return w.Code
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(waSecret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (r *waRig) send(body string) int { return r.post(body, sign(body)) }

func TestWAInboundSignature(t *testing.T) {
	r := newWARig(t, true)
	b := waBody("A1", "628123456789@s.whatsapp.net", "#cek")
	if c := r.post(b, ""); c != http.StatusUnauthorized {
		t.Fatalf("missing signature: %d", c)
	}
	if c := r.post(b, "sha256=deadbeef"); c != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", c)
	}
	if c := r.post(b+" ", sign(b)); c != http.StatusUnauthorized {
		t.Fatalf("body changed after signing: %d", c)
	}
	if len(r.sent) != 0 {
		t.Fatal("replied to an unsigned message")
	}
	if c := r.send(b); c != http.StatusOK || len(r.sent["628123456789"]) != 1 {
		t.Fatalf("good signature: %d %v", c, r.sent)
	}
}

func TestWAInboundDisabledWithoutSecretOrSwitch(t *testing.T) {
	r := newWARig(t, false)
	b := waBody("A1", "628123456789@s.whatsapp.net", "#cek")
	if c := r.send(b); c != http.StatusNotFound {
		t.Fatalf("not configured: %d", c)
	}
	if err := r.q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: "alt_wga_webhook_secret", Value: waSecret}); err != nil {
		t.Fatal(err)
	}
	if c := r.send(b); c != http.StatusNotFound {
		t.Fatalf("secret but switch off: %d", c)
	}
}

func TestWAInboundIgnores(t *testing.T) {
	r := newWARig(t, true)
	for i, b := range []string{
		strings.Replace(waBody("B1", "628123456789@s.whatsapp.net", "cek"), `"message"`, `"message.ack"`, 1),
		strings.Replace(waBody("B2", "628123456789@s.whatsapp.net", "cek"), `"is_from_me":false`, `"is_from_me":true`, 1),
		strings.Replace(waBody("B3", "628123456789@s.whatsapp.net", "cek"), `"chat_id":"628123456789@s.whatsapp.net"`, `"chat_id":"1203@g.us"`, 1),
		waBody("B4", "628123456789@s.whatsapp.net", "  "),
		waBody("B5", "2515111@lid", "cek"),
	} {
		if c := r.send(b); c != http.StatusOK {
			t.Fatalf("case %d: %d", i, c)
		}
	}
	if len(r.sent) != 0 {
		t.Fatalf("replied to ignored messages: %v", r.sent)
	}
}

func TestWAInboundUnknownAndOwnData(t *testing.T) {
	r := newWARig(t, true)
	r.send(waBody("C1", "628999000@s.whatsapp.net", "#cek"))
	got := strings.Join(r.sent["628999000"], "")
	if got == "" || strings.Contains(got, "Budi") || strings.Contains(got, "Ani") {
		t.Fatalf("unknown sender reply: %q", got)
	}
	r.send(waBody("C2", "628123456789@s.whatsapp.net", "#cek"))
	r.send(waBody("C3", "628123456789@s.whatsapp.net", "#cari ani"))
	for _, x := range r.sent["628123456789"] {
		if strings.Contains(x, "Ani") || strings.Contains(x, "628555000111") {
			t.Fatalf("customer saw another customer: %q", x)
		}
	}
	if !strings.Contains(r.sent["628123456789"][0], "Budi Santoso") {
		t.Fatalf("cek: %v", r.sent["628123456789"])
	}
}

func TestWAInboundDedupe(t *testing.T) {
	r := newWARig(t, true)
	b := waBody("D1", "628123456789@s.whatsapp.net", "#cek")
	r.send(b)
	r.send(b)
	if n := len(r.sent["628123456789"]); n != 1 {
		t.Fatalf("a retried message was answered %d times", n)
	}
}

func TestWAInboundRateLimit(t *testing.T) {
	r := newWARig(t, true)
	for i := 0; i < waRateLimit+5; i++ {
		r.send(waBody("E"+string(rune('a'+i)), "628123456789@s.whatsapp.net", "#cek"))
	}
	if n := len(r.sent["628123456789"]); n != waRateLimit {
		t.Fatalf("answered %d, want the limit %d", n, waRateLimit)
	}
}

func TestWAInboundIgnoresChat(t *testing.T) {
	r := newWARig(t, true)
	r.send(waBody("F1", "628123456789@s.whatsapp.net", "halo, besok bisa ketemu?"))
	r.send(waBody("F2", "628999000@s.whatsapp.net", "cek"))
	r.send(waBody("F3", "628999000@s.whatsapp.net", "123456"))
	if len(r.sent) != 0 {
		t.Fatalf("ordinary chat got replies: %v", r.sent)
	}
}
