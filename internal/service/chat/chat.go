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
	Kind          Kind
	CustomerID    int64  // KindCustomer
	AdminID       int64  // KindAdmin
	Role          string // KindAdmin: SuperAdmin, Admin, Agent, Sales, Report
	Phone         string // the sender's phone as Identify matched it; write confirmations are bound to it
	OwnCustomerID int64  // KindAdmin: the one customer with the same phone, 0 when none or several
}

// asCustomer is the customer side of an admin who also has a customer account on the same phone.
func (c Caller) asCustomer() (Caller, bool) {
	return Caller{Kind: KindCustomer, CustomerID: c.OwnCustomerID, Phone: c.Phone}, c.Kind == KindAdmin && c.OwnCustomerID != 0
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
	NASFresh  func(ip string) time.Duration // how recent a NAS packet counts as online; nil = 15 minutes

	// wa-write
	Confirm    *ConfirmStore                                    // pending writes; one store for all requests
	Billing    Biller                                           // nil = recharge is off
	Disconnect func(ctx context.Context, username string) error // drops RADIUS sessions; nil = off

	// wa-pay
	StaleFor func(nas string) int64                                                         // radius.StaleFor; nil = 15 minutes
	Notify   func(ctx context.Context, phone, text string) error                            // sends WhatsApp to an admin; nil = reports are not forwarded
	Pay      func(ctx context.Context, c db.Customer, p db.Plan) (db.PaymentRequest, error) // starts a gateway payment; nil = no gateway
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

// Identify finds the sender by phone. An active admin wins over a customer with the same number; that
// customer (only when exactly one has the number) is kept as OwnCustomerID, so the admin can still use
// the customer commands for their own account.
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
	var admin *Caller
	for _, a := range admins {
		if a.Status == "Active" && db.CanonicalPhone(a.Phone) == p {
			admin = &Caller{Kind: KindAdmin, AdminID: a.ID, Role: a.Role, Phone: p}
			break
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
	if admin != nil {
		if len(ids) == 1 {
			admin.OwnCustomerID = ids[0]
		}
		return *admin, nil
	}
	switch len(ids) {
	case 0:
		return Caller{}, ErrUnknown
	case 1:
		return Caller{Kind: KindCustomer, CustomerID: ids[0], Phone: p}, nil
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
	if out, ok := s.confirm(ctx, c, strings.TrimSpace(text)); ok {
		return out, "confirm"
	}
	text = menuPick(c, text)
	t, args, ok := find(c, text)
	if !ok && !denied(c, text) {
		if cc, own := c.asCustomer(); own { // admin first, then the admin's own customer account
			if t, args, ok := find(cc, menuPick(cc, text)); ok {
				return t.Run(s, ctx, cc, args), t.Name
			}
		}
	}
	if !ok {
		if denied(c, text) {
			return s.t("You are not allowed to use this command."), ""
		}
		return s.t("Command not known. Send #bantuan to see the commands."), ""
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

// denied: an admin asked for an admin command that exists but their role may not use. Customers never
// learn that admin commands exist.
func denied(c Caller, text string) bool {
	if c.Kind != KindAdmin {
		return false
	}
	word, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	word = strings.ToLower(strings.Trim(word, ".,!?/"))
	return slices.ContainsFunc(tools, func(t Tool) bool {
		return t.Kind == KindAdmin && (t.Name == word || slices.Contains(t.Aliases, word))
	})
}

// allowed is the permission gate: the tool is for this kind of caller and, for admins, this role.
func allowed(t Tool, c Caller) bool {
	return t.Kind == c.Kind && (c.Kind != KindAdmin || len(t.Roles) == 0 || slices.Contains(t.Roles, c.Role))
}
