package db

import (
	"context"
	"database/sql"
	"strings"
)

// CanonicalPhone is the form a phone number is compared in: digits only, with a leading 0 (the
// national form) replaced by 62, so "0812-3456-789", "+62 812 3456 789" and "628123456789" are one
// number. Comparisons normalise both sides; NormalisePhones rewrites the stored rows at startup.
func CanonicalPhone(p string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, p)
	switch {
	case d == "0":
		return ""
	case strings.HasPrefix(d, "0"):
		return "62" + d[1:]
	}
	return d
}

// NormalisePhones rewrites every stored customer phone to CanonicalPhone in one transaction and
// returns how many changed. Idempotent: a second run changes nothing. It runs at startup so rows
// saved as typed ("0812-...") match the canonical search and lists.
// ponytail: loads all phone rows into memory; batch it if the customer table grows to tens of thousands.
func NormalisePhones(ctx context.Context, conn *sql.DB) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id, phone FROM customers WHERE phone <> ''")
	if err != nil {
		return 0, err
	}
	type fix struct {
		id    int64
		phone string
	}
	var fixes []fix
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return 0, err
		}
		if c := CanonicalPhone(p); c != p {
			fixes = append(fixes, fix{id, c})
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, f := range fixes {
		if _, err := tx.ExecContext(ctx, "UPDATE customers SET phone = ? WHERE id = ?", f.phone, f.id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(fixes), nil
}
