package device

import (
	"context"
	"fmt"
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
