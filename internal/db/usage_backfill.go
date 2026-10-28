package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const usageBackfilledKey = "usage_daily_backfilled"

// BackfillUsageDaily fills radius_usage_daily from the existing radius_sessions, once. It is Go and not
// migration SQL because day boundaries are midnights in the app time zone, which is a setting that SQL
// cannot see. A session that stays inside one day goes to that day; a longer one (started_at to its stop,
// or its last update when still open) is split by the seconds it spent in each day, the last day taking the
// rounding rest. The settings flag makes it run once; it is set in the same transaction as the rows.
func BackfillUsageDaily(ctx context.Context, conn *sql.DB, loc *time.Location) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := New(tx)
	if _, err := q.GetSetting(ctx, usageBackfilledKey); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT username, started_at, COALESCE(stopped_at, updated_at), input_octets, output_octets
		FROM radius_sessions WHERE input_octets > 0 OR output_octets > 0`)
	if err != nil {
		return err
	}
	var segs []AddRadiusUsageDailyParams
	for rows.Next() {
		var user string
		var start, end, in, out int64
		if err := rows.Scan(&user, &start, &end, &in, &out); err != nil {
			rows.Close()
			return err
		}
		segs = append(segs, splitDays(user, start, end, in, out, loc)...)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, g := range segs {
		if err := q.AddRadiusUsageDaily(ctx, g); err != nil {
			return err
		}
	}
	if err := q.UpsertSettingRaw(ctx, UpsertSettingRawParams{Key: usageBackfilledKey, Value: "1"}); err != nil {
		return err
	}
	return tx.Commit()
}

// splitDays divides a session's octets over the local days between start and end by seconds in each day.
func splitDays(user string, start, end, in, out int64, loc *time.Location) []AddRadiusUsageDailyParams {
	end = max(end, start)
	const f = "2006-01-02"
	if t0 := time.Unix(start, 0).In(loc); t0.Format(f) == time.Unix(end, 0).In(loc).Format(f) {
		return []AddRadiusUsageDailyParams{{Username: user, Day: t0.Format(f), InputOctets: in, OutputOctets: out}}
	}
	total := end - start
	var res []AddRadiusUsageDailyParams
	var din, dout int64
	for cur := start; cur < end; {
		t := time.Unix(cur, 0).In(loc)
		next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc).Unix()
		i, o := in*(next-cur)/total, out*(next-cur)/total // ponytail: octets x seconds overflows int64 above ~100 TB in one session
		if next >= end {
			next, i, o = end, in-din, out-dout
		}
		din, dout = din+i, dout+o
		res = append(res, AddRadiusUsageDailyParams{Username: user, Day: t.Format(f), InputOctets: i, OutputOctets: o})
		cur = next
	}
	return res
}
