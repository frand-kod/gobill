package billing

// Reconcile of an API-mode router's accounts with gobill (import and follow-gobill/follow-router).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
)

// Reconcile row kinds.
const (
	ReconSame   = "same"    // on both sides, nothing differs
	ReconDiffer = "differs" // on both sides, profile or password differs
	ReconGobill = "gobill"  // only in gobill (an active subscription the router lacks)
	ReconRouter = "router"  // only on the router
)

// Reconcile actions an admin can choose per row.
const (
	ActNone   = "none"   // same rows: nothing to do
	ActSkip   = "skip"   // leave the row as it is
	ActPush   = "push"   // gobill -> router (differs, gobill)
	ActRouter = "router" // router -> gobill, the password (differs)
	ActImport = "import" // router-only -> new customer
)

// ReconRow is one account compared by username. Detail says what differs and never holds a password.
type ReconRow struct {
	Kind, Type, Name, Detail string
	SubID, PlanID            int64 // gobill side, 0 for router-only rows
	Router                   device.Account
}

// Key identifies the row in a form: the same username can exist as PPPoE and as hotspot.
func (r ReconRow) Key() string { return r.Type + ":" + r.Name }

// DefaultAction is what a row gets unless the admin chooses otherwise: follow gobill, push the missing,
// leave the router-only (never deleted, never imported without a choice).
func (r ReconRow) DefaultAction() string {
	switch r.Kind {
	case ReconDiffer, ReconGobill:
		return ActPush
	case ReconRouter:
		return ActSkip
	}
	return ActNone
}

// Reconcile reads the router (read-only) and compares it with gobill by username. A RADIUS router is
// refused (ErrRadiusRouter), an offline one stops with an error before anything is compared.
func (s *Service) Reconcile(ctx context.Context, routerID int64) ([]ReconRow, error) {
	r, err := s.Q.GetRouter(ctx, routerID)
	if err != nil {
		return nil, err
	}
	if _, err := s.pushPlans(ctx, routerID); err != nil {
		return nil, err
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = rt.Ping(pctx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("router offline: %w", err)
	}
	accts, err := rt.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read router: %w", err)
	}
	onRouter := map[string]device.Account{}
	for _, a := range accts {
		onRouter[a.Type+":"+a.Name] = a
	}
	ids, err := s.Q.ListActiveSubscriptionIDsByRouter(ctx, sql.NullInt64{Int64: routerID, Valid: true})
	if err != nil {
		return nil, err
	}
	var rows []ReconRow
	for _, id := range ids {
		sub, err := s.Q.GetSubscription(ctx, id)
		if err != nil {
			return nil, err
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return nil, err
		}
		if p.Device == "Radius" || (p.Type != "PPPoE" && p.Type != "Hotspot") {
			continue
		}
		c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
		if err != nil {
			return nil, err
		}
		row := ReconRow{Type: p.Type, Name: routerName(c, p.Type), SubID: sub.ID, PlanID: p.ID}
		a, ok := onRouter[row.Key()]
		row.Router = a
		delete(onRouter, row.Key())
		switch {
		case !ok:
			row.Kind = ReconGobill
		default:
			var diff []string
			if a.Profile != p.Name {
				diff = append(diff, "profile: router "+a.Profile+", gobill "+p.Name)
			}
			if len(c.SecretEnc) > 0 {
				pw, err := secret.Open(s.Key, c.SecretEnc)
				if err != nil {
					return nil, fmt.Errorf("decrypt customer secret: %w", err)
				}
				if string(pw) != a.Password {
					diff = append(diff, "password")
				}
			}
			row.Kind, row.Detail = ReconSame, ""
			if len(diff) > 0 {
				row.Kind, row.Detail = ReconDiffer, strings.Join(diff, "; ")
			}
		}
		rows = append(rows, row)
	}
	for _, a := range onRouter {
		rows = append(rows, ReconRow{Kind: ReconRouter, Type: a.Type, Name: a.Name, Router: a, Detail: "profile: " + a.Profile})
	}
	slices.SortFunc(rows, func(a, b ReconRow) int { return strings.Compare(a.Key(), b.Key()) })
	return rows, nil
}

// ReconResult tallies an apply; Failures hold reasons without passwords.
type ReconResult struct {
	Pushed, Imported, Followed, Skipped int
	Failures                            []string
}

// ReconApply reads the router again and carries out act(row) for each row, so a stale page cannot act
// on rows that changed. The read is all-or-nothing (offline or RADIUS stops before any write); the
// writes go row by row and a failing row does not stop the others. hash makes the portal password
// hash of an imported customer. The router password only goes through secret.Seal, never into a message.
func (s *Service) ReconApply(ctx context.Context, routerID, adminID int64, act func(ReconRow) string, hash func(string) (string, error)) (ReconResult, error) {
	var res ReconResult
	rows, err := s.Reconcile(ctx, routerID)
	if err != nil {
		return res, err
	}
	planned := map[int64]bool{} // plans already sent, so a pushed subscription finds its profile
	for _, row := range rows {
		a := act(row)
		fail := func(err error) { res.Failures = append(res.Failures, row.Type+" "+row.Name+": "+err.Error()) }
		switch {
		case a == ActSkip || a == ActNone || (a == ActPush && row.Kind == ReconRouter):
			res.Skipped++
		case a == ActPush:
			if !planned[row.PlanID] {
				p, err := s.Q.GetPlan(ctx, row.PlanID)
				if err == nil {
					err = s.SyncPlan(ctx, "update", p, p.Name)
				}
				if err != nil {
					fail(err)
					continue
				}
				planned[row.PlanID] = true
			}
			if err := s.SyncSubscription(ctx, row.SubID, adminID); err != nil {
				fail(err)
			} else {
				res.Pushed++
			}
		case a == ActRouter && row.Kind == ReconDiffer:
			sub, err := s.Q.GetSubscription(ctx, row.SubID)
			if err != nil {
				fail(err)
				continue
			}
			enc, err := secret.Seal(s.Key, []byte(row.Router.Password))
			if err == nil {
				err = s.Q.SetCustomerSecret(ctx, db.SetCustomerSecretParams{SecretEnc: enc, ID: sub.CustomerID})
			}
			if err != nil {
				fail(err)
			} else {
				res.Followed++
			}
		case a == ActImport && row.Kind == ReconRouter:
			if err := s.reconImport(ctx, row, adminID, hash); err != nil {
				fail(err)
			} else {
				res.Imported++
			}
		default:
			fail(errors.New("choice does not fit this row"))
		}
	}
	if err := s.logAdmin(ctx, s.Q, adminID, "router.reconcile", fmt.Sprintf("router %d: %d pushed, %d imported, %d followed router", routerID, res.Pushed, res.Imported, res.Followed)); err != nil {
		return res, err
	}
	return res, nil
}

// reconImport creates a customer from a router-only account. No subscription: price and validity are
// not on the router, the admin activates a plan afterwards.
func (s *Service) reconImport(ctx context.Context, row ReconRow, adminID int64, hash func(string) (string, error)) error {
	if _, err := s.Q.FindLoginNameFold(ctx, row.Name); err == nil {
		return errors.New("a customer with this username already exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	enc, err := secret.Seal(s.Key, []byte(row.Router.Password))
	if err != nil {
		return err
	}
	ph, err := hash(row.Router.Password)
	if err != nil {
		return err
	}
	_, err = s.Q.CreateCustomer(ctx, db.CreateCustomerParams{Username: row.Name, PasswordHash: ph, Fullname: row.Name,
		ServiceType: row.Type, SecretEnc: enc, Status: "Active", CreatedBy: sql.NullInt64{Int64: adminID, Valid: adminID != 0}})
	return err
}
