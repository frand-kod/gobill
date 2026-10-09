package importer

import (
	"fmt"
	"strconv"
	"strings"
)

// fields imports tbl_customers_fields (PHP "user attributes") into custom_fields /
// customer_field_values, same names. "Expired Date" (the Period billing day) goes to
// customers.billing_day instead. gobill reads "* Bill" and "Invoice" from the custom fields
// when it charges a recharge (internal/billing/attrs.go).
func (m *imp) fields() error {
	t := m.table("customer_fields")
	ids := map[string]int64{}
	return m.each(t, "SELECT id, customer_id, field_name, COALESCE(field_value,'') FROM tbl_customers_fields ORDER BY id", 4, func(r row) {
		if !m.custIDs[r.i(1)] {
			t.skip(r[0], "customer deleted in old system")
			return
		}
		name, val := strings.TrimSpace(r[2]), strings.TrimSpace(r[3])
		if name == "" {
			t.skip(r[0], "empty field name")
			return
		}
		if name == "Expired Date" {
			day, err := strconv.Atoi(val)
			if err != nil || day < 1 || day > 31 {
				t.skip(r[0], fmt.Sprintf("Expired Date %q is not a day 1-31", val))
				return
			}
			if m.put(t, r[0], "UPDATE customers SET billing_day = ? WHERE id = ?", day, r.i(1)) {
				t.Notes = append(t.Notes, fmt.Sprintf("customer %s: Expired Date %d -> billing_day", r[1], day))
			}
			return
		}
		fid, ok := ids[name]
		if !ok {
			err := m.tx.QueryRowContext(m.ctx, `INSERT INTO custom_fields (name, type) VALUES (?, 'text')
				ON CONFLICT (name) DO UPDATE SET name = excluded.name RETURNING id`, name).Scan(&fid)
			if err != nil {
				t.skip(r[0], err)
				return
			}
			ids[name] = fid
		}
		m.put(t, r[0], `INSERT INTO customer_field_values (customer_id, field_id, value) VALUES (?, ?, ?)
			ON CONFLICT (customer_id, field_id) DO UPDATE SET value = excluded.value`, r.i(1), fid, val)
	})
}
