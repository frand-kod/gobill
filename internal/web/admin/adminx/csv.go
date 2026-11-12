package adminx

import (
	"encoding/csv"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/web/httpx"
)

// DateRange reads ?from and ?to (YYYY-MM-DD, billing zone); to is inclusive. Unix 0 = open end.
func (s *Base) DateRange(r *http.Request) (from, to string, fromTS, toTS int64) {
	parse := func(k string) (string, time.Time, bool) {
		v := r.URL.Query().Get(k)
		t, err := time.ParseInLocation("2006-01-02", v, s.Location())
		return v, t, err == nil
	}
	if v, t, ok := parse("from"); ok {
		from, fromTS = v, t.Unix()
	}
	if v, t, ok := parse("to"); ok {
		to, toTS = v, t.AddDate(0, 0, 1).Unix()
	}
	return
}

func (s *Base) WriteCSV(w http.ResponseWriter, name string, head []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write(head)
	for _, rec := range rows {
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}
