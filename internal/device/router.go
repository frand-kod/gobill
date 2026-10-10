package device

import (
	"context"
	"fmt"
	"strconv"
)

// Ping connects to the router and returns its identity, proving host, port and login work.
func (r Router) Ping(ctx context.Context) (string, error) {
	rows, err := r.exec(ctx, []string{"/system/identity/print"})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0]["name"], nil
}

// Pool is an /ip/pool entry; Ranges is e.g. "10.10.10.2-10.10.10.254".
type Pool struct{ Name, Ranges string }

const ipPool = "/ip/pool"

// SyncPool applies an admin pool change. op is "add", "update" or "remove"; oldName is the
// pool's name before an update. An update of a pool missing on the router adds it.
func (r Router) SyncPool(ctx context.Context, op, oldName string, p Pool) error {
	switch op {
	case "add":
		_, err := r.exec(ctx, []string{ipPool + "/add", "=name=" + p.Name, "=ranges=" + p.Ranges})
		return err
	case "update":
		id, err := firstID(ctx, r.exec, ipPool, "name", oldName)
		if err != nil {
			return err
		}
		if id == "" {
			return r.SyncPool(ctx, "add", "", p)
		}
		_, err = r.exec(ctx, []string{ipPool + "/set", "=numbers=" + id, "=name=" + p.Name, "=ranges=" + p.Ranges})
		return err
	case "remove":
		return removeWhere(ctx, r.exec, ipPool, "name", p.Name)
	}
	return fmt.Errorf("unknown pool sync op %q", op)
}

// Health is the live state of a router: its resource counters and how many users are logged in.
type Health struct {
	Version, Board, Uptime string
	CPULoad                int64 // percent
	FreeMem, TotalMem      int64 // bytes
	Hotspot, PPP           int   // active sessions
}

// Health reads /system/resource and counts the active hotspot and PPP sessions.
func (r Router) Health(ctx context.Context) (Health, error) {
	var h Health
	res, err := r.exec(ctx, []string{"/system/resource/print"})
	if err != nil {
		return h, err
	}
	if len(res) > 0 {
		m := res[0]
		h.Version, h.Board, h.Uptime = m["version"], m["board-name"], m["uptime"]
		h.CPULoad, _ = strconv.ParseInt(m["cpu-load"], 10, 64)
		h.FreeMem, _ = strconv.ParseInt(m["free-memory"], 10, 64)
		h.TotalMem, _ = strconv.ParseInt(m["total-memory"], 10, 64)
	}
	hs, err := r.exec(ctx, []string{"/ip/hotspot/active/print", "=.proplist=.id"})
	if err != nil {
		return h, err
	}
	pp, err := r.exec(ctx, []string{"/ppp/active/print", "=.proplist=.id"})
	if err != nil {
		return h, err
	}
	h.Hotspot, h.PPP = len(hs), len(pp)
	return h, nil
}
