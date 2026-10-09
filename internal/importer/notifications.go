package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// notifKeys are the PHP notifications.json keys gobill has a template for (settings notif_<key>).
var notifKeys = map[string]bool{"expired": true, "reminder_7_day": true, "reminder_3_day": true, "reminder_1_day": true,
	"invoice_paid": true, "invoice_balance": true, "welcome_message": true, "balance_send": true, "balance_received": true}

// MapNotifications turns PHP's uploads/notifications.json into settings (notif_<key> -> text).
// Keys gobill has no template for (e.g. email_invoice) are returned in ignored.
func MapNotifications(data []byte) (settings map[string]string, ignored []string, err error) {
	var in map[string]any
	if err = json.Unmarshal(data, &in); err != nil {
		return nil, nil, err
	}
	settings = map[string]string{}
	for k, v := range in {
		if s, ok := v.(string); ok && notifKeys[k] {
			if s != "" {
				settings["notif_"+k] = s
			}
		} else {
			ignored = append(ignored, k)
		}
	}
	sort.Strings(ignored)
	return settings, ignored, nil
}

// notifications imports the templates from Options.Notifications. A missing path or file is
// reported in the summary, never an error.
func (m *imp) notifications() error {
	t := m.table("notifications")
	if m.o.Notifications == "" {
		t.Notes = append(t.Notes, "not imported: pass --notifications=<phpnuxbill>/system/uploads/notifications.json to keep your message templates")
		return nil
	}
	data, err := os.ReadFile(m.o.Notifications)
	if errors.Is(err, fs.ErrNotExist) {
		t.Notes = append(t.Notes, m.o.Notifications+" not found: PHP uses its built-in English templates, so gobill's defaults apply")
		return nil
	} else if err != nil {
		return fmt.Errorf("notifications: %w", err)
	}
	set, ignored, err := MapNotifications(data)
	if err != nil {
		t.Notes = append(t.Notes, fmt.Sprintf("%s is not valid JSON: %v", m.o.Notifications, err))
		return nil
	}
	t.Read = len(set) + len(ignored)
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m.put(t, k, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", k, set[k])
	}
	for _, k := range ignored {
		t.Notes = append(t.Notes, "template "+k+" has no equivalent in gobill, skipped")
	}
	return nil
}
