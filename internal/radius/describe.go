package radius

import "fmt"

// Attrs lists the reply attributes an accepted Decision gives the NAS, as "Name = value" lines
// (for the Test login page; the UDP reply is built in handleAuth).
func (d Decision) Attrs() []string {
	var out []string
	add := func(k string, v any) { out = append(out, fmt.Sprintf("%s = %v", k, v)) }
	if d.Plan.PlanType == "PPPoE" {
		if d.Plan.PoolName.Valid && d.Plan.PoolName.String != "" {
			add("Framed-Pool", d.Plan.PoolName.String)
		}
		if d.Cust.PppoeIp != "" {
			add("Framed-IP-Address", d.Cust.PppoeIp)
		}
	}
	if d.Rate != "" {
		add("Mikrotik-Rate-Limit", d.Rate)
	}
	if d.AddressList != "" {
		add("Mikrotik-Address-List", d.AddressList)
	}
	if d.HasTotal {
		add("Mikrotik-Total-Limit", d.TotalLeft)
	}
	if d.Timeout > 0 {
		add("Session-Timeout", d.Timeout)
	}
	if d.Plan.IdleTimeout.Valid && d.Plan.IdleTimeout.Int64 > 0 {
		add("Idle-Timeout", d.Plan.IdleTimeout.Int64)
	}
	if d.Interim > 0 {
		add("Acct-Interim-Interval", d.Interim)
	}
	return out
}
