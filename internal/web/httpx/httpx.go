// Package httpx holds HTTP and string helpers shared by the web packages.
package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const MaxBulkIDs = 1000

// ParseIDs reads the posted "ids" strictly: positive integers only, duplicates dropped, 1..maxBulkIDs of them.
func ParseIDs(r *http.Request) ([]int64, bool) {
	r.ParseForm()
	vals := r.PostForm["ids"]
	if len(vals) == 0 || len(vals) > MaxBulkIDs {
		return nil, false
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, v := range vals {
		id, ok := PosInt(v)
		if !ok {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

// PosInt parses a positive integer.
func PosInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

func OneOf(v string, list ...string) bool { return Contains(list, v) }

func IsUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func IsFK(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

func PathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// FormVals returns the trimmed values of the named form fields.
func FormVals(r *http.Request, names ...string) map[string]string {
	m := map[string]string{}
	for _, n := range names {
		m[n] = strings.TrimSpace(r.PostFormValue(n))
	}
	return m
}

// SafeRedirect returns u only when it is an absolute http(s) URL.
func SafeRedirect(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return p.String()
}

// CSVSafe defuses spreadsheet formula injection.
func CSVSafe(rec []string) {
	for i, f := range rec {
		if f != "" && strings.ContainsRune("=+-@\t\r", rune(f[0])) {
			rec[i] = "'" + f
		}
	}
}

func Contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// PeerAddr parses host:port (or a bare host) into an address; IPv4-mapped IPv6 becomes IPv4.
func PeerAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
