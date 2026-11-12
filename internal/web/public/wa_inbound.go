package public

// POST /wa/inbound: the GOWA webhook for incoming WhatsApp messages (command mode). The HMAC
// signature over the raw body is the only credential. The sender is told apart by phone number,
// which GOWA reports but cannot prove, so every write tool needs a one-time code (see chat/confirm.go).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/service/chat"
	"github.com/frand-kod/gobill/internal/service/payorder"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/core"
)

// nasFresh is the NAS online window of the network health page.
func nasFresh(ip string) time.Duration {
	return max(network.NASFreshFor, time.Duration(radius.StaleFor(ip))*time.Second)
}

const (
	waMaxBody   = 64 << 10
	waRateLimit = 20 // commands per phone per core.LoginWindow
	waSeenTTL   = 10 * time.Minute
	waSeenMax   = 5000
)

type waInbound struct {
	Event   string `json:"event"`
	Payload struct {
		ID       string `json:"id"`
		ChatID   string `json:"chat_id"`
		From     string `json:"from"`
		FromMe   bool   `json:"is_from_me"`
		Body     string `json:"body"`
		FromName string `json:"from_name"`
	} `json:"payload"`
}

// waPhone is the digits of a "628123@s.whatsapp.net" id ("628123:12@..." for a linked device), or ""
// for anything else, such as an @lid id that carries no phone number.
func waPhone(jid string) string {
	num, host, ok := strings.Cut(jid, "@")
	if !ok || host != "s.whatsapp.net" {
		return ""
	}
	num, _, _ = strings.Cut(num, ":")
	for _, r := range num {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return num
}

// waSig checks X-Hub-Signature-256 ("sha256=<hex>") over the raw body.
func waSig(secret string, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// waFirstSeen records a message id and says false when it was already seen in the last waSeenTTL.
// ponytail: in memory, lost on restart and capped at waSeenMax ids; a retry after a restart is answered twice.
func (s *Handlers) waFirstSeen(id string) bool {
	s.waMu.Lock()
	defer s.waMu.Unlock()
	now := time.Now()
	if t, ok := s.waSeen[id]; ok && now.Sub(t) < waSeenTTL {
		return false
	}
	if s.waSeen == nil || len(s.waSeen) >= waSeenMax {
		for k, t := range s.waSeen {
			if now.Sub(t) >= waSeenTTL {
				delete(s.waSeen, k)
			}
		}
		if s.waSeen == nil || len(s.waSeen) >= waSeenMax {
			s.waSeen = map[string]time.Time{}
		}
	}
	s.waSeen[id] = now
	return true
}

func (s *Handlers) waInbound(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "wa inbound", err)
		return
	}
	secret := st["alt_wga_webhook_secret"]
	if secret == "" || st["wa_inbound"] != "yes" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, waMaxBody))
	if err != nil || !waSig(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var m waInbound
	if json.Unmarshal(body, &m) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p := m.Payload
	phone := waPhone(p.From)
	text := strings.TrimSpace(p.Body)
	switch {
	case m.Event != "message", p.FromMe, strings.HasSuffix(p.ChatID, "@g.us"), text == "":
	case phone == "":
		slog.Debug("wa inbound: sender has no phone number", "id", p.ID)
	case p.ID != "" && !s.waFirstSeen(p.ID):
		slog.Debug("wa inbound: duplicate", "id", p.ID)
	default:
		s.waWG.Add(1)
		go func() { // answer after the webhook has returned, so GOWA does not wait on the reply
			defer s.waWG.Done()
			s.waCommand(phone, text)
		}()
	}
	w.WriteHeader(http.StatusOK)
}

// waCommand identifies the sender, runs the command and sends the reply.
func (s *Handlers) waCommand(phone, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	masked := phone[:min(3, len(phone))] + "***" + phone[max(len(phone)-3, 0):]
	if s.Reserve("wa:"+db.CanonicalPhone(phone), waRateLimit) < 0 {
		slog.Info("wa inbound: rate limited", "phone", masked)
		return
	}
	st, err := s.LoadSettings(ctx)
	if err != nil {
		slog.Error("wa inbound: settings", "err", err)
		return
	}
	send := s.waSend
	if send == nil {
		n, err := notify.Load(ctx, s.Queries)
		if err != nil {
			slog.Error("wa inbound: notifier", "err", err)
			return
		}
		send = n.WhatsApp
	}
	svc := &chat.Service{Q: s.Queries, T: s.T, PortalURL: core.CustomerLoginURL(st), Loc: s.Location(),
		NASFresh: nasFresh, StaleFor: radius.StaleFor, Notify: send, Pay: s.waPay(st),
		Confirm: waConfirm, Disconnect: s.waDisconnect}
	if s.Billing != nil { // a nil *billing.Service must not become a non-nil Biller
		svc.Billing = s.Billing
	}
	reply, tool := "", ""
	c, err := svc.Identify(ctx, phone)
	switch err {
	case nil:
		reply, tool = svc.Reply(ctx, c, text)
		s.waLog(ctx, c, tool)
	case chat.ErrUnknown, chat.ErrAmbiguous:
		reply = svc.ReplyUnknown(err)
	default:
		slog.Error("wa inbound: identify", "err", err)
		return
	}
	slog.Info("wa inbound: command", "phone", masked, "tool", tool)
	if err := send(ctx, phone, reply); err != nil {
		slog.Warn("wa inbound: reply failed", "phone", masked, "err", err)
	}
}

// waConfirm holds the pending write commands. One store for all requests.
var waConfirm = chat.NewConfirmStore()

// waDisconnect drops the open RADIUS sessions of a user, as the admin sessions page does.
func (s *Handlers) waDisconnect(ctx context.Context, username string) error {
	return radius.Driver{Q: s.Queries, Key: s.SecretKey}.Disconnect(ctx, device.Customer{Username: username}, "")
}

// waPay starts a gateway payment for the chat tool bayar. It uses the same order code as the portal
// (payorder.Create) with the same price rule. nil when no gateway is set.
func (s *Handlers) waPay(st map[string]string) func(context.Context, db.Customer, db.Plan) (db.PaymentRequest, error) {
	g, err := s.Gateway(st)
	base := strings.TrimRight(st["app_url"], "/")
	if err != nil || g == nil || base == "" {
		return nil
	}
	return func(ctx context.Context, c db.Customer, p db.Plan) (db.PaymentRequest, error) {
		chans, err := s.ActiveChannels(ctx, g)
		if err != nil || len(chans) == 0 {
			return db.PaymentRequest{}, chat.ErrNoGateway
		}
		amount := p.Price
		if s.Billing != nil {
			if amount, err = s.Billing.OrderPrice(ctx, c.ID, amount); err != nil {
				return db.PaymentRequest{}, err
			}
		}
		return payorder.Create(ctx, s.Queries, g, payorder.Order{Customer: c, PlanID: p.ID, PlanName: p.Name, Amount: amount,
			Channel: chans[0].Code, ReturnBase: base, CallbackBase: base})
	}
}

// waLog puts the command in the activity log with source wa. The message text is not stored.
func (s *Handlers) waLog(ctx context.Context, c chat.Caller, tool string) {
	typ, id := "customer", c.CustomerID
	if c.Kind == chat.KindAdmin {
		typ, id = "admin", c.AdminID
	}
	if tool == "" {
		tool = "unknown"
	}
	if err := s.Queries.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: typ, ActorID: id, Action: "wa." + tool, Description: "source=wa"}); err != nil {
		slog.Error("wa inbound: activity log", "err", err)
	}
}
