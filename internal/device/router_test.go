package device

import (
	"context"
	"testing"
)

func TestPoolSyncAndPing(t *testing.T) {
	f := &fake{ids: map[string]string{"/ip/pool|name=old": "*5"}}
	r := Router{Exec: f.exec}
	must(t, r.SyncPool(ctx, "add", "", Pool{"p1", "10.0.0.2-10.0.0.9"}))
	must(t, r.SyncPool(ctx, "update", "old", Pool{"new", "10.0.1.2-10.0.1.9"}))
	must(t, r.SyncPool(ctx, "update", "gone", Pool{"g", "1.1.1.1-1.1.1.2"}))
	must(t, r.SyncPool(ctx, "remove", "", Pool{Name: "old"}))
	f.check(t,
		"/ip/pool/add =name=p1 =ranges=10.0.0.2-10.0.0.9",
		"/ip/pool/print =.proplist=.id ?name=old",
		"/ip/pool/set =numbers=*5 =name=new =ranges=10.0.1.2-10.0.1.9",
		"/ip/pool/print =.proplist=.id ?name=gone",
		"/ip/pool/add =name=g =ranges=1.1.1.1-1.1.1.2",
		"/ip/pool/print =.proplist=.id ?name=old",
		"/ip/pool/remove =numbers=*5")
	id, err := Router{Exec: func(context.Context, []string) ([]map[string]string, error) {
		return []map[string]string{{"name": "MikroTik"}}, nil
	}}.Ping(ctx)
	if err != nil || id != "MikroTik" {
		t.Fatal(id, err)
	}
}
