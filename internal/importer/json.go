package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	jsonTable = regexp.MustCompile(`^tbl_[a-z0-9_]+$`)
	jsonCol   = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// OpenJSON loads a PHPNuxBill Database Status > Backup file into an in-memory SQLite with the
// same tbl_* names, so Run reads it like MySQL. Every column is TEXT except id (phone keeps its 0).
func OpenJSON(ctx context.Context, path string) (*sql.DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var data map[string][]map[string]any
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:phpjson%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one connection keeps the memory DB alive
	if err := loadJSON(ctx, db, data); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func loadJSON(ctx context.Context, db *sql.DB, data map[string][]map[string]any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, rows := range data {
		// empty or unknown tables are left out, so the import reports them as missing
		if !jsonTable.MatchString(name) || len(rows) == 0 {
			continue
		}
		set := map[string]bool{}
		for _, r := range rows {
			for c := range r {
				set[c] = true
			}
		}
		cols := make([]string, 0, len(set))
		for c := range set {
			if !jsonCol.MatchString(c) {
				return fmt.Errorf("%s: bad column name %q", name, c)
			}
			cols = append(cols, c)
		}
		sort.Strings(cols)
		defs, quoted := make([]string, len(cols)), make([]string, len(cols))
		for i, c := range cols {
			typ := "TEXT"
			if c == "id" {
				typ = "INTEGER"
			}
			defs[i], quoted[i] = `"`+c+`" `+typ, `"`+c+`"`
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE "%s" (%s)`, name, strings.Join(defs, ", "))); err != nil {
			return err
		}
		ph := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
		st, err := tx.PrepareContext(ctx, fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`, name, strings.Join(quoted, ", "), ph))
		if err != nil {
			return err
		}
		for _, r := range rows {
			args := make([]any, len(cols))
			for i, c := range cols {
				args[i] = jsonVal(r[c])
			}
			if _, err := st.ExecContext(ctx, args...); err != nil {
				st.Close()
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		st.Close()
	}
	return tx.Commit()
}

// jsonVal: null stays NULL, numbers and bools keep their text form.
func jsonVal(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
