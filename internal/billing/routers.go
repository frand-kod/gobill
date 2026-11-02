package billing

// Router connections, ping, pool sync and health check.

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
	"net"
	"strconv"
	"time"
)

const (
	routerFailMax = 2                   // consecutive failed pings before a router is declared offline (~10 min at the 5 min tick)
	routerDownKey = "alert_router_down" // settings key: JSON array of router ids declared offline
)

// routerState is the in-memory alert state of RouterCheck: failed pings in a row, and the routers
// declared offline. The offline set is persisted so a restart neither re-alerts nor loses the recovery.
type routerState struct {
	mu     sync.Mutex
	loaded bool
	fails  map[int64]int
	down   map[int64]bool
}

// load reads the persisted offline set once. Caller holds mu.
func (rs *routerState) load(ctx context.Context, q *db.Queries) {
	if rs.loaded {
		return
	}
	rs.loaded = true
	rs.fails, rs.down = map[int64]int{}, map[int64]bool{}
	v, _ := q.GetSetting(ctx, routerDownKey)
	var ids []int64
	_ = json.Unmarshal([]byte(v), &ids)
	for _, id := range ids {
		rs.down[id] = true
	}
}

// save persists the offline set. Caller holds mu.
func (rs *routerState) save(ctx context.Context, q *db.Queries) {
	ids := []int64{}
	for id := range rs.down {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	b, _ := json.Marshal(ids)
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: routerDownKey, Value: string(b)}); err != nil {
		slog.Error("save router alert state", "err", err)
	}
}

// downHosts returns the hosts of enabled routers currently declared offline. AlertJob uses it to
// leave the NAS-silent and mass-drop alerts of such a router to the router alert.
func (s *Service) downHosts(ctx context.Context) map[string]bool {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	s.rt.load(ctx, s.Q)
	hosts := map[string]bool{}
	if len(s.rt.down) == 0 {
		return hosts
	}
	rs, err := s.Q.ListEnabledRouters(ctx)
	if err != nil {
		return hosts
	}
	for _, r := range rs {
		if s.rt.down[r.ID] {
			hosts[r.Host] = true
		}
	}
	return hosts
}

// RouterCheck pings every enabled router and stores online/last_seen_at. A router is declared
// offline (stored online=0, one alert) only after routerFailMax failed pings in a row, so a blip
// is invisible; one alert when it comes back, and only if the offline alert was sent. A router
// that is down at start is declared after the same number of failures. router_check = "no" turns
// it off; unset counts as yes. It is the job.Run body of the old router_check cron.
func (s *Service) RouterCheck(ctx context.Context) error {
	if setting(ctx, s.Q, "router_check") == "no" {
		return nil
	}
	rs, err := s.Q.ListEnabledRouters(ctx)
	if err != nil {
		return err
	}
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	s.rt.load(ctx, s.Q)
	for _, r := range rs {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, perr := s.Ping(pctx, r)
		cancel()
		up := perr == nil
		wasDown := s.rt.down[r.ID]
		if up {
			s.rt.fails[r.ID] = 0
		} else {
			s.rt.fails[r.ID]++
		}
		declared := wasDown || (!up && s.rt.fails[r.ID] >= routerFailMax)
		p := db.SetRouterStatusParams{ID: r.ID, Online: sql.NullInt64{Valid: true}}
		switch {
		case up:
			p.Online.Int64 = 1
			p.LastSeenAt = sql.NullInt64{Int64: s.now().Unix(), Valid: true}
		case !declared:
			continue // a first failure: UI and alerts keep the previous state
		}
		if err := s.Q.SetRouterStatus(ctx, p); err != nil {
			return err
		}
		var msg string
		switch {
		case up && wasDown:
			delete(s.rt.down, r.ID)
			msg = fmt.Sprintf("Router %s kembali terhubung", r.Name)
		case !up && !wasDown:
			s.rt.down[r.ID] = true
			msg = fmt.Sprintf("Router %s tidak terjangkau: %s", r.Name, perr)
		default:
			continue
		}
		s.rt.save(ctx, s.Q)
		if err := s.Alert(ctx, msg); err != nil {
			slog.Error("router alert", "router", r.Name, "err", err)
		}
	}
	return nil
}

// routerConn builds the connection info of a stored router; RouterFor overrides it in tests.
func (s *Service) routerConn(r db.Router) (device.Router, error) {
	if s.RouterFor != nil {
		return s.RouterFor(r)
	}
	pw, err := secret.Open(s.Key, r.PasswordEnc)
	if err != nil {
		return device.Router{}, fmt.Errorf("decrypt router password: %w", err)
	}
	return device.Router{Addr: net.JoinHostPort(r.Host, strconv.FormatInt(r.Port, 10)), User: r.Username, Pass: string(pw), TLS: r.Tls == 1,
		Pin: r.CertPin, OnPin: func(fp string) {
			// only fills an empty pin, so concurrent first connects keep the first one
			if err := s.Q.PinRouterCert(context.Background(), db.PinRouterCertParams{CertPin: fp, ID: r.ID}); err != nil {
				slog.Error("pin router certificate", "router", r.Name, "err", err)
			}
		}}, nil
}

// Ping connects to the router and returns its identity.
func (s *Service) Ping(ctx context.Context, r db.Router) (string, error) {
	rt, err := s.routerConn(r)
	if err != nil {
		return "", err
	}
	return rt.Ping(ctx)
}

// Health reads the live state of a stored router (resources and active sessions).
func (s *Service) Health(ctx context.Context, r db.Router) (device.Health, error) {
	rt, err := s.routerConn(r)
	if err != nil {
		return device.Health{}, err
	}
	return rt.Health(ctx)
}

// SyncPool pushes an admin pool change to the pool's router (op: add, update, remove).
func (s *Service) SyncPool(ctx context.Context, op string, p db.Pool, oldName string) error {
	r, err := s.Q.GetRouter(ctx, p.RouterID)
	if err != nil {
		return err
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return err
	}
	return rt.SyncPool(ctx, op, oldName, device.Pool{Name: p.Name, Ranges: p.RangeIp})
}
