package radius

import (
	"context"
	"database/sql"
	"errors"
	"github.com/frand-kod/gobill/internal/settings"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"layeh.com/radius"

	"github.com/frand-kod/gobill/internal/clock"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

type Server struct {
	Q        *db.Queries
	DB       *sql.DB // set: accounting writes are group-committed (acctbatch.go); nil: written one by one
	Key      []byte
	Trusted  func() bool           // clock guard; false = do not reject for expiry
	AuthAddr string                // e.g. ":1812"
	AcctAddr string                // e.g. ":1813"
	Now      clock.Func            // default time.Now
	Loc      func() *time.Location // the app time zone for daily usage; nil = UTC
	// Redeem activates an unused voucher for a customer (billing.Service.RedeemVoucher).
	// nil disables hotspot voucher login.
	Redeem func(ctx context.Context, code string, customerID int64) error
	// Start begins a start_on_first_login subscription at the customer's first login (billing.StartPending).
	Start func(ctx context.Context, customerID int64) error
	// Kick drops one session on its NAS when shared_users_policy is kick_oldest. nil = Disconnect-Request (CoA).
	Kick func(ctx context.Context, sess db.RadiusSession) error

	userFails       failLimiter // failed password checks per username
	dups            dupCache    // answered requests, for retransmits
	unknownMu       sync.Mutex
	unknownSeen     map[string]time.Time // ip -> last unknown-NAS warning
	unknownLast     time.Time            // last warning for any IP while unknownSeen is full
	maWarned        sync.Map             // ip string -> time.Time of last Message-Authenticator warning
	badSecretWarned sync.Map             // ip string -> time.Time of last bad-secret warning
	busy            atomic.Int64         // packets being handled, see shed
	acctOnce        sync.Once            // starts acctWriter
	acctQ           chan acctJob         // accounting writes waiting for the next commit
	octs            octCache             // last counters per open session, for usage deltas
	nasMu           sync.Mutex
	nasRows         []nasEntry      // listNAS cache
	nasAt           time.Time       // when nasRows was read; zero = never
	Settings        *settings.Store // shared settings cache; nil = made on first use
}

func (s *Server) now() time.Time { return s.Now.Now() }

func (s *Server) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc()
	}
	return time.UTC
}

// ListenAndServe runs both servers until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	auth := &radius.PacketServer{Addr: s.AuthAddr, SecretSource: s, Handler: radius.HandlerFunc(s.HandleAuth)}
	acct := &radius.PacketServer{Addr: s.AcctAddr, SecretSource: s, Handler: radius.HandlerFunc(s.HandleAcct)}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, ps := range []*radius.PacketServer{auth, acct} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.serve(ps); err != nil && !errors.Is(err, radius.ErrServerShutdown) {
				errs <- err
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	auth.Shutdown(sh)
	acct.Shutdown(sh)
	wg.Wait()
	return err
}

// ServeConn serves auth (or acct) requests on a socket the caller made, such as one inside the WireGuard
// tunnel, until ctx is done. It closes pc.
func (s *Server) ServeConn(ctx context.Context, pc net.PacketConn, acct bool) error {
	h := radius.HandlerFunc(s.HandleAuth)
	if acct {
		h = s.HandleAcct
	}
	ps := &radius.PacketServer{SecretSource: s, Handler: h}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ps.Shutdown(sh)
	}()
	if err := ps.Serve(&nasConn{PacketConn: pc, s: s}); !errors.Is(err, radius.ErrServerShutdown) {
		return err
	}
	return nil
}

// store is the shared settings Store; a standalone Server makes its own.
func (s *Server) store() *settings.Store {
	s.nasMu.Lock()
	defer s.nasMu.Unlock()
	if s.Settings == nil {
		s.Settings = settings.New(s.Q)
	}
	return s.Settings
}

// settings is the settings table, read-only. A read error gives an empty map (defaults) and is retried
// next packet.
func (s *Server) settings(ctx context.Context) map[string]string {
	m, err := s.store().View(ctx)
	if err != nil {
		slog.Error("radius: read settings", "err", err)
		return map[string]string{}
	}
	return m
}

// maxBusy caps the packets handled at once (auth and accounting together). The library starts a
// goroutine per packet; past the server's capacity they queued without bound, memory grew to
// hundreds of MB, and every answer came after the NAS had given up (3 s), so nobody got in.
// Dropping the excess at once keeps the rest fast; the NAS retransmits what was dropped.
// ponytail: fixed; about half a second of work at the measured 1750 logins/s on two cores. 256 gave
// lower latency but served fewer packets under overload (bench/radius, mixed at 8000/s).
const maxBusy = 1000

// shed reports whether this packet must be dropped; when it returns false the caller holds a busy slot.
func (s *Server) shed() bool {
	if s.busy.Add(1) > maxBusy {
		s.busy.Add(-1)
		metrics.Inc("radius_packets_shed_total")
		return true
	}
	return false
}
