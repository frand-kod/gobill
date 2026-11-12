package chat

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"slices"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const (
	confirmTTL      = 5 * time.Minute
	confirmMaxWrong = 3
	confirmDigits   = 6
)

// pendingAction is one write waiting for its code. run does the work and re-checks everything it
// depends on (price, plan, customer), so nothing stored here is trusted for money.
type pendingAction struct {
	phone   string   // canonical phone of the sender that asked
	roles   []string // roles allowed to run it, checked again at confirm time
	code    string
	expires time.Time
	wrong   int
	run     func(ctx context.Context, adminID int64) string
}

// ConfirmStore holds at most one pending write per admin.
// ponytail: in memory, lost on restart, which only cancels pending actions; nobody is charged.
type ConfirmStore struct {
	mu sync.Mutex
	m  map[int64]*pendingAction
}

func NewConfirmStore() *ConfirmStore { return &ConfirmStore{m: map[int64]*pendingAction{}} }

// newCode is confirmDigits random digits from crypto/rand.
func newCode() (string, error) {
	b := make([]byte, confirmDigits)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = '0' + b[i]%10 // 256 is not a multiple of 10: the slight bias is harmless for a 5 minute code
	}
	return string(b), nil
}

func isCode(text string) bool {
	if len(text) < 4 || len(text) > confirmDigits {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ask stores the action for the caller (replacing an earlier one) and returns the reply with the code.
func (s *Service) ask(c Caller, roles []string, preview string, run func(ctx context.Context, adminID int64) string) string {
	if s.Confirm == nil {
		return s.failed()
	}
	code, err := newCode()
	if err != nil {
		return s.failed()
	}
	s.Confirm.mu.Lock()
	s.Confirm.m[c.AdminID] = &pendingAction{phone: db.CanonicalPhone(c.Phone), roles: roles, code: code,
		expires: s.now().Add(confirmTTL), run: run}
	s.Confirm.mu.Unlock()
	return preview + "\n" + s.t("Reply %s within 5 minutes to confirm. A new command cancels this one.", code)
}

// confirm handles a message that is only digits. It reports false when the text is not for it
// (not an admin, not a code, or nothing pending), so the normal command lookup goes on.
func (s *Service) confirm(ctx context.Context, c Caller, text string) (string, bool) {
	if s.Confirm == nil || c.Kind != KindAdmin || !isCode(text) {
		return "", false
	}
	st := s.Confirm
	st.mu.Lock()
	p, ok := st.m[c.AdminID]
	if !ok {
		st.mu.Unlock()
		return "", false
	}
	switch {
	case !s.now().Before(p.expires):
		delete(st.m, c.AdminID)
		st.mu.Unlock()
		return s.t("The code has expired. Send the command again."), true
	case p.phone == "" || p.phone != db.CanonicalPhone(c.Phone):
		st.mu.Unlock() // not consumed: only the phone that asked may use the code
		return s.t("This code is not valid."), true
	case subtle.ConstantTimeCompare([]byte(p.code), []byte(text)) != 1:
		p.wrong++
		if p.wrong >= confirmMaxWrong {
			delete(st.m, c.AdminID)
			st.mu.Unlock()
			return s.t("Wrong code three times. The command is cancelled."), true
		}
		st.mu.Unlock()
		return s.t("Wrong code. Try again."), true
	}
	delete(st.m, c.AdminID) // single use: gone before it runs
	st.mu.Unlock()

	a, err := s.Q.GetAdmin(ctx, c.AdminID)
	if err != nil || a.Status != "Active" || db.CanonicalPhone(a.Phone) != p.phone || !slices.Contains(p.roles, a.Role) {
		return s.t("You are not allowed to do this."), true
	}
	return p.run(ctx, a.ID), true
}
