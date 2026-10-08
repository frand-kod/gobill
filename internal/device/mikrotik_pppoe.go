package device

import (
	"context"
	"errors"
	"strings"
)

type MikrotikPPPoE struct{ ex execFunc }

func NewMikrotikPPPoE(r Router) *MikrotikPPPoE { return &MikrotikPPPoE{ex: r.exec} }

const (
	pppSecret  = "/ppp/secret"
	pppProfile = "/ppp/profile"
	pppActive  = "/ppp/active"
)

func (d *MikrotikPPPoE) secretID(ctx context.Context, c Customer) (string, error) {
	id, err := firstID(ctx, d.ex, pppSecret, "name", c.Username)
	if err != nil || id != "" || c.PPPoEUsername == "" {
		return id, err
	}
	return firstID(ctx, d.ex, pppSecret, "name", c.PPPoEUsername)
}

func (d *MikrotikPPPoE) AddCustomer(ctx context.Context, c Customer, p Plan) error {
	pass := c.Password
	if c.PPPoEPassword != "" {
		pass = c.PPPoEPassword
	}
	if pass == "" {
		return errors.New("customer has no router password")
	}
	id, err := d.secretID(ctx, c)
	if err != nil {
		return err
	}
	useIP := c.PPPoEIP != "" && !p.IsExpiredProfile
	comment := c.FullName + " | " + c.Email + " | " + strings.Join(c.Bills, ", ")
	if id == "" {
		s := []string{pppSecret + "/add", "=service=pppoe", "=profile=" + p.Name,
			"=comment=" + comment, "=password=" + pass, "=name=" + c.pppUser()}
		if useIP {
			s = append(s, "=remote-address="+c.PPPoEIP)
		}
		_, err = d.ex(ctx, s)
		return err
	}
	s := []string{pppSecret + "/set", "=numbers=" + id, "=password=" + pass, "=name=" + c.pppUser()}
	if useIP {
		s = append(s, "=remote-address="+c.PPPoEIP)
	}
	s = append(s, "=profile="+p.Name, "=comment="+comment)
	if _, err = d.ex(ctx, s); err != nil {
		return err
	}
	if !useIP {
		_, err = d.ex(ctx, []string{pppSecret + "/unset", "=.id=" + id, "=value-name=remote-address"})
	}
	return err
}

func (d *MikrotikPPPoE) removeActive(ctx context.Context, c Customer) error {
	if err := removeWhere(ctx, d.ex, pppActive, "name", c.Username); err != nil {
		return err
	}
	if c.PPPoEUsername != "" {
		return removeWhere(ctx, d.ex, pppActive, "name", c.PPPoEUsername)
	}
	return nil
}

func (d *MikrotikPPPoE) RemoveCustomer(ctx context.Context, c Customer, p Plan) error {
	if p.ExpiredPlan != nil {
		e := *p.ExpiredPlan
		e.IsExpiredProfile = true
		if err := d.AddCustomer(ctx, c, e); err != nil {
			return err
		}
		return d.removeActive(ctx, c)
	}
	if err := removeWhere(ctx, d.ex, pppSecret, "name", c.Username); err != nil {
		return err
	}
	if c.PPPoEUsername != "" {
		if err := removeWhere(ctx, d.ex, pppSecret, "name", c.PPPoEUsername); err != nil {
			return err
		}
	}
	return d.removeActive(ctx, c)
}

func (d *MikrotikPPPoE) ChangeUsername(ctx context.Context, p Plan, from, to string) error {
	id, err := firstID(ctx, d.ex, pppSecret, "name", from)
	if err != nil || id == "" {
		return err
	}
	if _, err := d.ex(ctx, []string{pppSecret + "/set", "=numbers=" + id, "=name=" + to}); err != nil {
		return err
	}
	return removeWhere(ctx, d.ex, pppActive, "name", from)
}

func (p Plan) localAddr() string {
	if p.PoolLocalIP != "" {
		return p.PoolLocalIP
	}
	return p.Pool
}

func (d *MikrotikPPPoE) AddPlan(ctx context.Context, p Plan) error {
	_, err := d.ex(ctx, []string{pppProfile + "/add",
		"=name=" + p.Name,
		"=local-address=" + p.localAddr(),
		"=remote-address=" + p.Pool,
		"=rate-limit=" + p.rateLimit(),
	})
	return err
}

func (d *MikrotikPPPoE) UpdatePlan(ctx context.Context, oldName string, p Plan) error {
	id, err := firstID(ctx, d.ex, pppProfile, "name", oldName)
	if err != nil {
		return err
	}
	if id == "" {
		return d.AddPlan(ctx, p)
	}
	_, err = d.ex(ctx, []string{pppProfile + "/set",
		"=numbers=" + id,
		"=name=" + p.Name,
		"=local-address=" + p.localAddr(),
		"=remote-address=" + p.Pool,
		"=rate-limit=" + p.rateLimit(),
		"=on-up=" + p.OnLogin,
		"=on-down=" + p.OnLogout,
	})
	return err
}

func (d *MikrotikPPPoE) RemovePlan(ctx context.Context, p Plan) error {
	return removeWhere(ctx, d.ex, pppProfile, "name", p.Name)
}

func (d *MikrotikPPPoE) IsOnline(ctx context.Context, c Customer, _ string) (bool, error) {
	id, err := firstID(ctx, d.ex, pppActive, "name", c.Username)
	if err != nil || id != "" || c.PPPoEUsername == "" {
		return id != "", err
	}
	id, err = firstID(ctx, d.ex, pppActive, "name", c.PPPoEUsername)
	return id != "", err
}

// Connect is a no-op: PPPoE sessions are initiated by the customer's modem.
func (d *MikrotikPPPoE) Connect(context.Context, Customer, string, string, string) error { return nil }

func (d *MikrotikPPPoE) Disconnect(ctx context.Context, c Customer, _ string) error {
	return d.removeActive(ctx, c)
}
