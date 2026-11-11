package importer

import (
	"net"
	"strings"

	"github.com/frand-kod/gobill/internal/secret"
)

func (m *imp) router() error {
	t := m.table("routers")
	return m.each(t, "SELECT id, name, ip_address, username, password, COALESCE(description,''), enabled FROM tbl_routers ORDER BY id", 7, func(r row) {
		host, port := r[2], "8728"
		if h, p, err := net.SplitHostPort(r[2]); err == nil {
			host, port = h, p
		}
		enc, err := secret.Seal(m.o.Key, []byte(r[4]))
		if err != nil {
			t.skip(r[0], err)
			return
		}
		if m.put(t, r[0], `INSERT INTO routers (id, name, host, port, username, password_enc, description, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], host, port, r[3], enc, r[5], r.i(6) != 0) {
			m.routers[r[1]] = r.i(0)
		}
	})
}

var devices = map[string]bool{"MikrotikHotspot": true, "MikrotikPppoe": true, "Dummy": true, "Radius": true}

func (m *imp) routerID(name string) (any, bool) {
	if id, ok := m.routers[name]; ok {
		return id, true
	}
	return nil, name == "" || strings.EqualFold(name, "radius")
}

func (m *imp) nas() error {
	t := m.table("nas")
	return m.each(t, "SELECT id, nasname, COALESCE(NULLIF(shortname,''), nasname), secret, COALESCE(description,'') FROM nas ORDER BY id", 5, func(r row) {
		enc, err := secret.Seal(m.o.Key, []byte(r[3]))
		if err != nil {
			t.skip(r[0], err)
			return
		}
		m.put(t, r[0], `INSERT INTO nas (id, name, ip, secret_enc, description) VALUES (?, ?, ?, ?, ?)`, r.i(0), r[2], r[1], enc, r[4])
	})
}
