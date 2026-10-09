package radius

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// fakeNAS listens for one Disconnect-Request, answers with reply, and hands over the raw bytes.
func fakeNAS(t *testing.T, reply radius.Code, cause ...uint32) (port string, got chan []byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	got = make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		req := append([]byte(nil), buf[:n]...)
		got <- req
		if p, err := radius.Parse(req, nasSecret); err == nil {
			resp := p.Response(reply)
			if len(cause) > 0 {
				resp.Add(101, radius.Attribute{0, 0, byte(cause[0] >> 8), byte(cause[0])})
			}
			if b, err := resp.Encode(); err == nil {
				pc.WriteTo(b, from)
			}
		}
	}()
	return strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port), got
}

func TestDisconnectRequest(t *testing.T) {
	e := setup(t)
	// the NAS row of setup() is 10.0.0.0/24; add the loopback fake
	sealed, _ := secret.Seal(e.s.Key, nasSecret)
	if _, err := e.q.CreateNAS(t.Context(), db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: sealed}); err != nil {
		t.Fatal(err)
	}
	sess := db.RadiusSession{Username: "alice", SessionID: "abc123", NasIp: "127.0.0.1", NasIpAttr: "192.168.99.1", FramedIp: "10.1.1.5"}

	port, got := fakeNAS(t, radius.CodeDisconnectACK)
	if err := Disconnect(t.Context(), e.q, e.s.Key, port, sess); err != nil {
		t.Fatal(err)
	}
	raw := <-got
	if !radius.IsAuthenticRequest(raw, nasSecret) {
		t.Fatal("request authenticator does not match the NAS secret")
	}
	p, _ := radius.Parse(raw, nasSecret)
	// RFC 5176: M-A over the packet with a zero authenticator, first attribute
	chk := append([]byte(nil), raw...)
	clear(chk[4:20])
	clear(chk[22:38])
	m := hmac.New(md5.New, nasSecret)
	m.Write(chk)
	if raw[20] != 80 || !bytes.Equal(m.Sum(nil), raw[22:38]) {
		t.Fatal("Disconnect-Request lacks a valid Message-Authenticator")
	}
	if p.Code != radius.CodeDisconnectRequest || rfc2865.UserName_GetString(p) != "alice" ||
		rfc2866.AcctSessionID_GetString(p) != "abc123" || !rfc2865.NASIPAddress_Get(p).Equal(net.ParseIP("192.168.99.1")) {
		t.Fatalf("bad packet: %+v", p)
	}

	port, _ = fakeNAS(t, radius.CodeDisconnectNAK, 403)
	if err := Disconnect(t.Context(), e.q, e.s.Key, port, sess); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("NAK error must name Error-Cause 403: %v", err)
	}
	// no NAS identity known: no NAS-IP-Address / NAS-Identifier on the wire
	port, got = fakeNAS(t, radius.CodeDisconnectACK)
	if err := Disconnect(t.Context(), e.q, e.s.Key, port, db.RadiusSession{Username: "alice", SessionID: "x", NasIp: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := radius.Parse(<-got, nasSecret); p.Get(rfc2865.NASIPAddress_Type) != nil || p.Get(rfc2865.NASIdentifier_Type) != nil {
		t.Fatal("NAS identification must be omitted when unknown")
	}
	port, _ = fakeNAS(t, radius.CodeDisconnectNAK)
	if err := Disconnect(t.Context(), e.q, e.s.Key, port, sess); err == nil {
		t.Fatal("NAK must be an error")
	}
	if err := Disconnect(t.Context(), e.q, e.s.Key, port, db.RadiusSession{NasIp: "192.168.9.9"}); err == nil {
		t.Fatal("unknown NAS must be an error")
	}
}

func TestUnknownNASWarnRateLimited(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	now := time.Now()
	s := &Server{Now: func() time.Time { return now }}
	ip := net.ParseIP("203.0.113.9")
	s.warnUnknown(ip)
	s.warnUnknown(ip)
	if n := strings.Count(buf.String(), "unknown NAS"); n != 1 {
		t.Fatalf("want 1 warning, got %d", n)
	}
	now = now.Add(6 * time.Minute)
	s.warnUnknown(ip)
	if n := strings.Count(buf.String(), "unknown NAS"); n != 2 {
		t.Fatalf("want 2 warnings after 5m, got %d", n)
	}
}
