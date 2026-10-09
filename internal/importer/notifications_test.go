package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestMapNotifications(t *testing.T) {
	set, ign, err := MapNotifications([]byte(`{"expired":"Halo [[name]]","reminder_1_day":"","email_invoice":"<html>","x":1}`))
	if err != nil || len(set) != 1 || set["notif_expired"] != "Halo [[name]]" || strings.Join(ign, ",") != "email_invoice,x" {
		t.Fatalf("%v %v %v", set, ign, err)
	}
	if _, _, err := MapNotifications([]byte(`nope`)); err == nil {
		t.Error("invalid json accepted")
	}
}

func TestNotificationsStep(t *testing.T) {
	ctx := context.Background()
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	tx, _ := lite.BeginTx(ctx, nil)
	defer tx.Rollback()
	path := filepath.Join(t.TempDir(), "notifications.json")
	run := func(p string) *Table {
		m := &imp{tx: tx, ctx: ctx, o: Options{Notifications: p}, rep: &Report{}}
		if err := m.notifications(); err != nil {
			t.Fatal(err)
		}
		return m.rep.Tables[0]
	}
	if tb := run(path); len(tb.Notes) != 1 || !strings.Contains(tb.Notes[0], "not found") {
		t.Errorf("missing file: %+v", tb)
	}
	os.WriteFile(path, []byte(`{"expired":"Paket [[package]] habis"}`), 0o600)
	if tb := run(path); tb.Loaded != 1 {
		t.Errorf("loaded: %+v", tb)
	}
	var v string
	tx.QueryRow(`SELECT value FROM settings WHERE key = 'notif_expired'`).Scan(&v)
	if v != "Paket [[package]] habis" {
		t.Errorf("value %q", v)
	}
}
