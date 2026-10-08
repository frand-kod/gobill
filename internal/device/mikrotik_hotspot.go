package device

import (
	"context"
	"strings"
)

type MikrotikHotspot struct{ ex execFunc }

func NewMikrotikHotspot(r Router) *MikrotikHotspot { return &MikrotikHotspot{ex: r.exec} }

const (
	hsUser    = "/ip/hotspot/user"
	hsProfile = "/ip/hotspot/user/profile"
	hsActive  = "/ip/hotspot/active"
)

func (d *MikrotikHotspot) AddCustomer(ctx context.Context, c Customer, p Plan) error {
	if err := removeWhere(ctx, d.ex, hsUser, "name", c.Username); err != nil {
		return err
	}
	if p.IsExpiredProfile {
		if err := removeWhere(ctx, d.ex, hsActive, "user", c.Username); err != nil {
			return err
		}
	}
	s := []string{hsUser + "/add",
		"=name=" + c.Username,
		"=profile=" + p.Name,
		"=password=" + c.Password,
		"=comment=" + c.FullName + " | " + strings.Join(c.Bills, ", "),
		"=email=" + c.Email,
	}
	if p.Limited {
		t := p.LimitType
		if t == "Time_Limit" || t == "Both_Limit" {
			if p.TimeUnit == "Hrs" {
				s = append(s, "=limit-uptime="+itoa(p.TimeLimit)+":00:00")
			} else {
				s = append(s, "=limit-uptime=00:"+itoa(p.TimeLimit)+":00")
			}
		}
		if t == "Data_Limit" || t == "Both_Limit" {
			if p.DataUnit == "GB" {
				s = append(s, "=limit-bytes-total="+itoa(p.DataLimit)+"000000000")
			} else {
				s = append(s, "=limit-bytes-total="+itoa(p.DataLimit)+"000000")
			}
		}
	}
	_, err := d.ex(ctx, s)
	return err
}

func (d *MikrotikHotspot) RemoveCustomer(ctx context.Context, c Customer, p Plan) error {
	if p.ExpiredPlan != nil {
		e := *p.ExpiredPlan
		e.IsExpiredProfile = true
		if err := d.AddCustomer(ctx, c, e); err != nil {
			return err
		}
		return removeWhere(ctx, d.ex, hsActive, "user", c.Username)
	}
	if err := removeWhere(ctx, d.ex, hsUser, "name", c.Username); err != nil {
		return err
	}
	return removeWhere(ctx, d.ex, hsActive, "user", c.Username)
}

func (d *MikrotikHotspot) ChangeUsername(ctx context.Context, p Plan, from, to string) error {
	id, err := firstID(ctx, d.ex, hsUser, "name", from)
	if err != nil || id == "" {
		return err
	}
	if _, err := d.ex(ctx, []string{hsUser + "/set", "=numbers=" + id, "=name=" + to}); err != nil {
		return err
	}
	return removeWhere(ctx, d.ex, hsActive, "user", from)
}

func (d *MikrotikHotspot) AddPlan(ctx context.Context, p Plan) error {
	_, err := d.ex(ctx, []string{hsProfile + "/add",
		"=name=" + p.Name,
		"=shared-users=" + itoa(p.SharedUsers),
		"=rate-limit=" + p.rateLimit(),
	})
	return err
}

func (d *MikrotikHotspot) UpdatePlan(ctx context.Context, oldName string, p Plan) error {
	id, err := firstID(ctx, d.ex, hsProfile, "name", oldName)
	if err != nil {
		return err
	}
	if id == "" {
		return d.AddPlan(ctx, p)
	}
	_, err = d.ex(ctx, []string{hsProfile + "/set",
		"=numbers=" + id,
		"=name=" + p.Name,
		"=shared-users=" + itoa(p.SharedUsers),
		"=rate-limit=" + p.rateLimit(),
		"=on-login=" + p.OnLogin,
		"=on-logout=" + p.OnLogout,
	})
	return err
}

func (d *MikrotikHotspot) RemovePlan(ctx context.Context, p Plan) error {
	return removeWhere(ctx, d.ex, hsProfile, "name", p.Name)
}

func (d *MikrotikHotspot) IsOnline(ctx context.Context, c Customer, _ string) (bool, error) {
	id, err := firstID(ctx, d.ex, hsActive, "user", c.Username)
	return id != "", err
}

func (d *MikrotikHotspot) Connect(ctx context.Context, c Customer, ip, mac, _ string) error {
	_, err := d.ex(ctx, []string{hsActive + "/login",
		"=user=" + c.Username, "=password=" + c.Password, "=ip=" + ip, "=mac-address=" + mac})
	return err
}

func (d *MikrotikHotspot) Disconnect(ctx context.Context, c Customer, _ string) error {
	return removeWhere(ctx, d.ex, hsActive, "user", c.Username)
}
