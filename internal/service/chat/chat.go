// Package chat is the shared tool list behind the chat clients (WhatsApp commands first). A tool is a
// keyword with Indonesian aliases, a rule for who may call it, and a function that returns reply text.
// Tools here only read. The caller is already identified; a customer tool only ever gets that
// customer's id, so it cannot reach other people's data.
package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/clock"
	"github.com/frand-kod/gobill/internal/db"
)

// Kind says who is talking.
type Kind int

const (
	KindCustomer Kind = iota + 1
	KindAdmin
)

// Caller is an identified sender.
type Caller struct {
	Kind       Kind
	CustomerID int64  // KindCustomer
	AdminID    int64  // KindAdmin
	Role       string // KindAdmin: SuperAdmin, Admin, Agent, Sales, Report
}

// Tool is one command. Roles lists the admin roles allowed (admin tools only; empty = every admin).
type Tool struct {
	Name    string
	Aliases []string
	Kind    Kind
	Roles   []string
	Run     func(s *Service, ctx context.Context, c Caller, args string) string
}

// Service holds what the tools read. Build one per request: PortalURL and T come from the current settings.
type Service struct {
	Q         *db.Queries
	T         func(key string) string // translates an English key; nil = as written
	PortalURL string                  // customer login address, "" when app_url is not set
	Loc       *time.Location          // app time zone for "today"; nil = Jakarta
	Now       clock.Func
}

var (
	// ErrUnknown: no admin or customer has this phone.
	ErrUnknown = errors.New("unknown sender")
	// ErrAmbiguous: several customers share this phone, so none can be trusted with the data.
	ErrAmbiguous = errors.New("phone shared by several customers")
)

func (s *Service) t(key string, a ...any) string {
	if s.T != nil {
		key = s.T(key)
	}
	if len(a) > 0 {
		return fmt.Sprintf(key, a...)
	}
	return key
}

func (s *Service) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc
	}
	return clock.Jakarta()
}

// Identify finds the sender by phone. An active admin wins over a customer with the same number.
// ponytail: scans all admins and all customer phones; add a phone lookup query if the customer table gets very large.
func (s *Service) Identify(ctx context.Context, phone string) (Caller, error) {
	p := db.CanonicalPhone(phone)
	if p == "" {
		return Caller{}, ErrUnknown
	}
	admins, err := s.Q.SearchAdmins(ctx, db.SearchAdminsParams{Q: sql.NullString{Valid: true}, Scope: "all", PageLimit: 1000})
	if err != nil {
		return Caller{}, err
	}
	for _, a := range admins {
		if a.Status == "Active" && db.CanonicalPhone(a.Phone) == p {
			return Caller{Kind: KindAdmin, AdminID: a.ID, Role: a.Role}, nil
		}
	}
	rows, err := s.Q.ListCustomerPhones(ctx)
	if err != nil {
		return Caller{}, err
	}
	var ids []int64
	for _, r := range rows {
		if db.CanonicalPhone(r.Phone) == p {
			ids = append(ids, r.ID)
		}
	}
	switch len(ids) {
	case 0:
		return Caller{}, ErrUnknown
	case 1:
		return Caller{Kind: KindCustomer, CustomerID: ids[0]}, nil
	}
	return Caller{}, ErrAmbiguous
}

// find is the tool a text asks for, or false when there is none the caller may use.
func find(c Caller, text string) (Tool, string, bool) {
	word, args, _ := strings.Cut(strings.TrimSpace(text), " ")
	word = strings.ToLower(strings.Trim(word, ".,!?/"))
	for _, t := range tools {
		if allowed(t, c) && (t.Name == word || slices.Contains(t.Aliases, word)) {
			return t, strings.TrimSpace(args), true
		}
	}
	return Tool{}, "", false
}

// Reply is the answer for an identified caller. The first word picks the tool, the rest is its argument.
// The second result is the tool name that ran, "" when the text matched none (for the log).
func (s *Service) Reply(ctx context.Context, c Caller, text string) (string, string) {
	t, args, ok := find(c, text)
	if !ok {
		return s.t("Command not known. Send bantuan to see the commands."), ""
	}
	return t.Run(s, ctx, c, args), t.Name
}

// ReplyUnknown is the answer to a sender nobody knows. It holds no data.
func (s *Service) ReplyUnknown(err error) string {
	if errors.Is(err, ErrAmbiguous) {
		return s.t("This number is used by more than one account. Please use the customer portal.")
	}
	return s.t("This number is not registered. Please contact your provider.")
}

// allowed is the permission gate: the tool is for this kind of caller and, for admins, this role.
func allowed(t Tool, c Caller) bool {
	return t.Kind == c.Kind && (c.Kind != KindAdmin || len(t.Roles) == 0 || slices.Contains(t.Roles, c.Role))
}
