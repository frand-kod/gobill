package billing

// Gradual migration of a router from local MikroTik accounts to RADIUS:
// 1 test a login through gobill's RADIUS path, 2 turn use-radius on, 3 disable the local accounts.
// Rollback undoes 3 and 2. Local accounts are only disabled, never removed, and no step touches an
// active session (no Disconnect, no remove): a running session moves to RADIUS on its next login.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

var (
	ErrMigOrder      = errors.New("this migration step is not allowed now")
	ErrMigNoAccounts = errors.New("the router has no local account of a gobill customer to migrate")
)

// Migration is where a router is: Step 0 not started (or rolled back), 1 login tested, 2 use-radius
// on, 3 local accounts disabled. At is the unix time of the last change, Kinds says what step 2
// switched: "p" PPP AAA, "h" hotspot profiles, or both.
type Migration struct {
	Step  int
	At    int64
	Kinds string
}

// migKey is the settings key; the value is "step:at:kinds".
func migKey(routerID int64) string { return "radius_migration_" + strconv.FormatInt(routerID, 10) }

// Migration reads the stored state of a router; no value means not started.
func (s *Service) Migration(ctx context.Context, routerID int64) Migration {
	f := strings.Split(setting(ctx, s.Q, migKey(routerID)), ":")
	if len(f) != 3 {
		return Migration{}
	}
	step, _ := strconv.Atoi(f[0])
	at, _ := strconv.ParseInt(f[1], 10, 64)
	return Migration{step, at, f[2]}
}

func (s *Service) setMigration(ctx context.Context, routerID int64, step int, kinds string) error {
	return s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: migKey(routerID), Value: fmt.Sprintf("%d:%d:%s", step, s.now().Unix(), kinds)})
}

// MigKinds says which use-radius settings the accounts need: "p" for PPPoE, "h" for hotspot.
func MigKinds(accts []device.LocalAccount) string {
	var p, h string
	for _, a := range accts {
		if a.Type == "PPPoE" {
			p = "p"
		} else {
			h = "h"
		}
	}
	return p + h
}

// MigrationCommands is the exact RouterOS change of step 2 (on) or of its rollback (off).
func MigrationCommands(kinds string, on bool) string {
	v := map[bool]string{true: "yes", false: "no"}[on]
	var out []string
	if strings.Contains(kinds, "p") {
		out = append(out, "/ppp aaa set use-radius="+v)
	}
	if strings.Contains(kinds, "h") {
		out = append(out, "/ip hotspot profile set [find] use-radius="+v)
	}
	return strings.Join(out, "\n")
}

// migAccounts reads the router and keeps the accounts that belong to an active gobill subscription
// of this router. Router-only accounts are left alone: after the switch nobody could log in with them.
func (s *Service) migAccounts(ctx context.Context, routerID int64) (device.Router, []device.LocalAccount, error) {
	r, err := s.Q.GetRouter(ctx, routerID)
	if err != nil {
		return device.Router{}, nil, err
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return rt, nil, err
	}
	ids, err := s.Q.ListActiveSubscriptionIDsByRouter(ctx, sql.NullInt64{Int64: routerID, Valid: true})
	if err != nil {
		return rt, nil, err
	}
	known := map[string]bool{}
	for _, id := range ids {
		sub, err := s.Q.GetSubscription(ctx, id)
		if err != nil {
			return rt, nil, err
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return rt, nil, err
		}
		if p.Device == "Radius" || (p.Type != "PPPoE" && p.Type != "Hotspot") {
			continue
		}
		c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
		if err != nil {
			return rt, nil, err
		}
		known[p.Type+":"+routerName(c, p.Type)] = true
	}
	all, err := rt.LocalAccounts(ctx)
	if err != nil {
		return rt, nil, fmt.Errorf("read router: %w", err)
	}
	var out []device.LocalAccount
	for _, a := range all {
		if known[a.Type+":"+a.Name] {
			out = append(out, a)
		}
	}
	return rt, out, nil
}

// MigrationAccounts is migAccounts for steps 1 to 3: a RADIUS-only router (ErrRadiusRouter) or one
// without a local account to migrate (ErrMigNoAccounts) is refused.
func (s *Service) MigrationAccounts(ctx context.Context, routerID int64) (device.Router, []device.LocalAccount, error) {
	if err := s.CheckRouterPush(ctx, routerID); err != nil {
		return device.Router{}, nil, err
	}
	rt, accts, err := s.migAccounts(ctx, routerID)
	if err == nil && len(accts) == 0 {
		err = ErrMigNoAccounts
	}
	return rt, accts, err
}

// MigrationTest is step 1. It offers the router's own password for the account "Type:Name" to auth,
// which runs gobill's RADIUS decision and returns the reject message ("" = accepted). The router is
// only read. An accepted login moves a router that has not started to step 1.
func (s *Service) MigrationTest(ctx context.Context, routerID int64, key string, auth func(user, password string) string) (string, error) {
	_, accts, err := s.MigrationAccounts(ctx, routerID)
	if err != nil {
		return "", err
	}
	for _, a := range accts {
		if a.Type+":"+a.Name != key {
			continue
		}
		reject := auth(a.Name, a.Password)
		if reject == "" && s.Migration(ctx, routerID).Step == 0 {
			err = s.setMigration(ctx, routerID, 1, MigKinds(accts))
		}
		return reject, err
	}
	return "", errors.New("account not found on the router")
}

// MigrationSwitch is step 2: use-radius on. tested says the admin passed step 1 in this session.
func (s *Service) MigrationSwitch(ctx context.Context, routerID, adminID int64, tested bool) error {
	if !tested || s.Migration(ctx, routerID).Step != 1 {
		return ErrMigOrder
	}
	rt, accts, err := s.MigrationAccounts(ctx, routerID)
	if err != nil {
		return err
	}
	kinds := MigKinds(accts)
	if err := rt.SetUseRadius(ctx, strings.Contains(kinds, "p"), strings.Contains(kinds, "h"), true); err != nil {
		return err
	}
	if err := s.setMigration(ctx, routerID, 2, kinds); err != nil {
		return err
	}
	return s.logAdmin(ctx, s.Q, adminID, "router.radius_switch", fmt.Sprintf("router %d: use-radius on (%s)", routerID, kinds))
}

// MigrationDisable is step 3: disable the enabled local accounts (never delete them). It returns how
// many it disabled; after a failure the state still moves to step 3 so that rollback re-enables them.
func (s *Service) MigrationDisable(ctx context.Context, routerID, adminID int64) (int, error) {
	st := s.Migration(ctx, routerID)
	if st.Step != 2 {
		return 0, ErrMigOrder
	}
	rt, accts, err := s.MigrationAccounts(ctx, routerID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range accts {
		if a.Disabled {
			continue
		}
		if err = rt.SetDisabled(ctx, a, true); err != nil {
			break
		}
		n++
	}
	if n > 0 {
		if serr := s.setMigration(ctx, routerID, 3, st.Kinds); serr != nil {
			return n, serr
		}
		if serr := s.logAdmin(ctx, s.Q, adminID, "router.radius_disable_local", fmt.Sprintf("router %d: %d local accounts disabled", routerID, n)); serr != nil {
			return n, serr
		}
	}
	return n, err
}

// MigrationRollback undoes what was done: it enables the disabled local accounts (step 3), turns
// use-radius off (step 2 and 3) and resets the state. Nothing is deleted and no session is touched.
// It works for a RADIUS-only router too, so an admin can always go back. It returns how many
// accounts it enabled.
func (s *Service) MigrationRollback(ctx context.Context, routerID, adminID int64) (int, error) {
	st := s.Migration(ctx, routerID)
	if st.Step == 0 {
		return 0, ErrMigOrder
	}
	n := 0
	if st.Step >= 2 {
		rt, accts, err := s.migAccounts(ctx, routerID)
		if err != nil {
			return 0, err
		}
		if st.Step == 3 {
			for _, a := range accts {
				if !a.Disabled {
					continue
				}
				if err := rt.SetDisabled(ctx, a, false); err != nil {
					return n, err
				}
				n++
			}
		}
		if err := rt.SetUseRadius(ctx, strings.Contains(st.Kinds, "p"), strings.Contains(st.Kinds, "h"), false); err != nil {
			return n, err
		}
	}
	if err := s.setMigration(ctx, routerID, 0, st.Kinds); err != nil {
		return n, err
	}
	return n, s.logAdmin(ctx, s.Q, adminID, "router.radius_rollback", fmt.Sprintf("router %d: back from step %d, %d local accounts enabled", routerID, st.Step, n))
}
