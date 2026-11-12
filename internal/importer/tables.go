package importer

import ()

var dataTables = []string{"activity_logs", "vouchers", "transactions", "subscriptions", "customers",
	"plans", "pools", "bandwidths", "routers", "nas", "admins", "settings"}

func (m *imp) table(name string) *Table {
	t := &Table{Name: name}
	m.rep.Tables = append(m.rep.Tables, t)
	return t
}
