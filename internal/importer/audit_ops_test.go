package importer

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// --force must be able to wipe a target that has agent batches.
func TestAuditOps2_ForceWipesAgentBatches(t *testing.T) {
	ctx := context.Background()
	my, _ := sql.Open("sqlite", ":memory:") // empty source: the import fails after the wipe
	defer my.Close()
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	if _, err := lite.Exec(`INSERT INTO admins (id, username, password_hash, role) VALUES (1, 'ag', 'x', 'Agent');
		INSERT INTO plans (id, name, type, price, validity, validity_unit) VALUES (1, 'P', 'Balance', 1000, 1, 'Days');
		INSERT INTO agent_batches (id, agent_id, plan_id, paid_count, bonus_count, unit_price) VALUES (1, 1, 1, 1, 0, 1000);
		INSERT INTO agent_ledger (batch_id, kind, amount) VALUES (1, 'debt', 1000);`); err != nil {
		t.Fatal(err)
	}
	_, err = Run(ctx, my, lite, Options{Force: true})
	if err != nil && !strings.Contains(err.Error(), "tbl_") {
		t.Fatalf("wipe failed before reading the source: %v", err)
	}
}
