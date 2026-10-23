// Command chrctl talks to a RouterOS API (the lab's CHR).
//
//	chrctl -a host:8728 -u admin -p PASS wait                 # block until the API logs in
//	chrctl -a ... run /ppp/active/print ?name=x               # one command, rows printed as k=v
//	chrctl -a ... script < lines                              # one command per line, words split on spaces
//
// A script line starting with "+" is idempotent: if "=name=X" already exists under the same path,
// the line becomes /set on that entry instead of /add. Lines starting with "#" are skipped.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-routeros/routeros/v3"
)

func run(c *routeros.Client, w []string) ([]map[string]string, error) {
	r, err := c.RunArgs(w)
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	for _, s := range r.Re {
		out = append(out, s.Map)
	}
	return out, nil
}

func ensure(c *routeros.Client, w []string) ([]string, error) {
	if !strings.HasSuffix(w[0], "/add") {
		return w, nil
	}
	path := strings.TrimSuffix(w[0], "/add")
	for _, x := range w[1:] {
		if strings.HasPrefix(x, "=name=") {
			rows, err := run(c, []string{path + "/print", "=.proplist=.id", "?" + x[1:]})
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				return append([]string{path + "/set", "=numbers=" + rows[0][".id"]}, w[1:]...), nil
			}
		}
	}
	return w, nil
}

func main() {
	addr := flag.String("a", "127.0.0.1:8728", "host:port")
	user := flag.String("u", "admin", "user")
	pass := flag.String("p", "", "password")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: chrctl [-a -u -p] wait|run CMD...|script")
		os.Exit(2)
	}
	if args[0] == "wait" {
		for end := time.Now().Add(240 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Second) {
			if c, err := routeros.Dial(*addr, *user, *pass); err == nil {
				c.Close()
				return
			}
		}
		fmt.Fprintln(os.Stderr, "chrctl: API not reachable / login refused")
		os.Exit(1)
	}
	c, err := routeros.Dial(*addr, *user, *pass)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chrctl:", err)
		os.Exit(1)
	}
	defer c.Close()
	show := func(rows []map[string]string) {
		for _, m := range rows {
			ks := make([]string, 0, len(m))
			for k := range m {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			var b strings.Builder
			for _, k := range ks {
				fmt.Fprintf(&b, "%s=%s ", k, m[k])
			}
			fmt.Println(strings.TrimSpace(b.String()))
		}
	}
	switch args[0] {
	case "run":
		rows, err := run(c, args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "chrctl:", err)
			os.Exit(1)
		}
		show(rows)
	case "script":
		sc := bufio.NewScanner(os.Stdin)
		fails := 0
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || l[0] == '#' {
				continue
			}
			w := strings.Fields(strings.TrimPrefix(l, "+"))
			for i := range w {
				w[i] = strings.ReplaceAll(w[i], "%20", " ") // fields cannot hold spaces otherwise
			}
			if l[0] == '+' {
				if w, err = ensure(c, w); err != nil {
					fmt.Fprintf(os.Stderr, "chrctl: %s: %v\n", l, err)
					fails++
					continue
				}
			}
			if _, err := run(c, w); err != nil {
				fmt.Fprintf(os.Stderr, "chrctl: %s: %v\n", l, err)
				fails++
			}
		}
		if fails > 0 {
			os.Exit(1)
		}
	}
}
