package importer

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestShortVoucherWarning(t *testing.T) {
	ctx := context.Background()
	my, _ := sql.Open("sqlite", ":memory:")
	my.SetMaxOpenConns(1)
	defer my.Close()
	if _, err := my.Exec(`CREATE TABLE tbl_voucher (id, code, id_plan, status, user, used_date, created_at, generated_by);
		INSERT INTO tbl_voucher VALUES (1, 'ab12', 1, '0', '', NULL, '2024-01-01 08:00:00', 0);
		INSERT INTO tbl_voucher VALUES (2, 'LONGCODE99', 1, '0', '', NULL, '2024-01-01 08:00:00', 0);
		INSERT INTO tbl_voucher VALUES (3, 'xy', 1, '1', 'u', '2024-01-02', '2024-01-01 08:00:00', 0);`); err != nil {
		t.Fatal(err)
	}
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	lite.Exec(`INSERT INTO plans (id, name, type, price, validity, validity_unit) VALUES (1, 'P', 'Balance', 1000, 1, 'Days')`)
	tx, _ := lite.BeginTx(ctx, nil)
	defer tx.Rollback()
	loc, _ := time.LoadLocation("Asia/Jakarta")
	m := &imp{my: my, tx: tx, ctx: ctx, o: Options{Loc: loc}, rep: &Report{}, plans: map[string]int64{}, customers: map[string]int64{}, admins: map[int64]bool{}, custIDs: map[int64]bool{}}
	if err := m.voucher(); err != nil {
		t.Fatal(err)
	}
	v := m.rep.Tables[0]
	if v.Loaded != 3 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "1 unused vouchers") {
		t.Fatalf("loaded %d notes %v skips %v", v.Loaded, v.Notes, v.Skips)
	}
}
