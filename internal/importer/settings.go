package importer

import (
	"slices"

	"github.com/frand-kod/gobill/internal/db"
)

// unsupportedFeatures are PHPNuxBill data gobill never imports.
var unsupportedFeatures = []string{"coupons", "ODP", "inbox"}

// unsupportedSettings are PHPNuxBill settings that change behaviour there but gobill never reads (paritas IM2).
// They are copied as they are, and reported only when the backup has them.
var unsupportedSettings = []string{"enable_coupons", "frrest_interim_update"}

func (m *imp) settings() error {
	t := m.table("settings")
	return m.each(t, "SELECT setting, COALESCE(value,'') FROM tbl_appconfig ORDER BY id", 2, func(r row) {
		if slices.Contains(unsupportedSettings, r[0]) {
			m.rep.Unsupported = append(m.rep.Unsupported, r[0])
		}
		m.put(t, r[0], "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", r[0], db.SealSettingWith(m.o.Key, r[0], r[1]))
	})
}
