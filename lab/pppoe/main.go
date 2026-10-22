// Command pppoe is a userspace PPPoE client (PADI..PADS, LCP, PAP/CHAP, IPCP) for the lab.
// Rootless podman cannot open /dev/ppp, so pppd cannot run; this speaks PPPoE over AF_PACKET instead.
// It stays up after IPCP until it gets SIGTERM (sends PADT) or the server drops the session.
//
//	pppoe -i eth0 -u USER -p PASS
//
// Exit codes: 0 stopped by signal, 1 authentication failed, 10 PADT from server, 11 LCP
// Terminate-Request from server, 12 timeout.
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

const (
	ethDisc, ethSess             = 0x8863, 0x8864
	padi, pado, padr, pads, padt = 0x09, 0x07, 0x19, 0x65, 0xa7
	lcp, pap, chap, ipcp         = 0xc021, 0xc023, 0xc223, 0x8021
)

var (
	fd    int
	ifi   *net.Interface
	peer  net.HardwareAddr
	sid   uint16
	magic = uint32(time.Now().UnixNano())
	bcast = net.HardwareAddr{255, 255, 255, 255, 255, 255}
)

func logf(f string, a ...any) { fmt.Printf("pppoe: "+f+"\n", a...) }
func die(code int, f string, a ...any) {
	logf(f, a...)
	os.Exit(code)
}
func htons(v uint16) uint16 { return v<<8 | v>>8 }

func sendEth(dst net.HardwareAddr, typ uint16, payload []byte) {
	b := append(append(append([]byte{}, dst...), ifi.HardwareAddr...), byte(typ>>8), byte(typ))
	sa := &syscall.SockaddrLinklayer{Ifindex: ifi.Index, Halen: 6}
	copy(sa.Addr[:], dst)
	syscall.Sendto(fd, append(b, payload...), 0, sa)
}

func pppoeHdr(code byte, id uint16, body []byte) []byte {
	h := []byte{0x11, code, byte(id >> 8), byte(id), byte(len(body) >> 8), byte(len(body))}
	return append(h, body...)
}

func tag(t uint16, v []byte) []byte {
	return append([]byte{byte(t >> 8), byte(t), byte(len(v) >> 8), byte(len(v))}, v...)
}

func sendPPP(proto uint16, data []byte) {
	sendEth(peer, ethSess, pppoeHdr(0, sid, append([]byte{byte(proto >> 8), byte(proto)}, data...)))
}

// ctl builds an LCP/IPCP style packet: code, id, length, data.
func ctl(code, id byte, data []byte) []byte {
	n := len(data) + 4
	return append([]byte{code, id, byte(n >> 8), byte(n)}, data...)
}

// recv returns one frame (ethertype, payload after the Ethernet header) or ok=false on timeout.
func recv(buf []byte) (typ uint16, src net.HardwareAddr, p []byte, ok bool) {
	n, _, err := syscall.Recvfrom(fd, buf, 0)
	if err != nil || n < 14 {
		return 0, nil, nil, false
	}
	if !bytes.Equal(buf[0:6], ifi.HardwareAddr) && !bytes.Equal(buf[0:6], bcast) {
		return 0, nil, nil, true
	}
	return binary.BigEndian.Uint16(buf[12:14]), net.HardwareAddr(append([]byte{}, buf[6:12]...)), buf[14:n], true
}

func tags(b []byte) map[uint16][]byte {
	m := map[uint16][]byte{}
	for len(b) >= 4 {
		t, l := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
		if 4+l > len(b) {
			break
		}
		m[t] = b[4 : 4+l]
		b = b[4+l:]
	}
	return m
}

func discover() {
	uniq := []byte{byte(os.Getpid()), 1, 2, 3}
	deadline := time.Now().Add(30 * time.Second)
	buf := make([]byte, 2048)
	var cookie []byte
	stage := padi
	last := time.Time{}
	for time.Now().Before(deadline) {
		if time.Since(last) > 2*time.Second {
			last = time.Now()
			if stage == padi {
				sendEth(bcast, ethDisc, pppoeHdr(padi, 0, append(tag(0x0101, nil), tag(0x0103, uniq)...)))
			} else {
				sendEth(peer, ethDisc, pppoeHdr(padr, 0, append(append(tag(0x0101, nil), tag(0x0103, uniq)...), cookie...)))
			}
		}
		typ, src, p, ok := recv(buf)
		if !ok || typ != ethDisc || len(p) < 6 {
			continue
		}
		code, id, l := p[1], binary.BigEndian.Uint16(p[2:]), int(binary.BigEndian.Uint16(p[4:]))
		if 6+l > len(p) {
			continue
		}
		t := tags(p[6 : 6+l])
		if !bytes.Equal(t[0x0103], uniq) {
			continue
		}
		switch {
		case code == pado && stage == padi:
			peer, stage = src, padr
			if c, ok := t[0x0104]; ok {
				cookie = tag(0x0104, c)
			}
			logf("PADO from %s ac=%q", src, t[0x0102])
			last = time.Time{}
		case code == pads && stage == padr:
			if id == 0 {
				die(1, "PADS with session 0: %s", t[0x0202])
			}
			sid = id
			logf("PADS session=%#x", sid)
			return
		}
	}
	die(12, "no PADO/PADS within 30s")
}

func main() {
	iface := flag.String("i", "eth0", "interface")
	user := flag.String("u", "", "username")
	pass := flag.String("p", "", "password")
	flag.Parse()
	var err error
	if ifi, err = net.InterfaceByName(*iface); err != nil {
		die(2, "%v", err)
	}
	if fd, err = syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(3))); err != nil {
		die(2, "socket: %v", err)
	}
	if err = syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(3), Ifindex: ifi.Index}); err != nil {
		die(2, "bind: %v", err)
	}
	tv := syscall.Timeval{Usec: 500000}
	_ = unsafe.Sizeof(tv)
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		if sid != 0 {
			sendEth(peer, ethDisc, pppoeHdr(padt, sid, nil))
		}
		logf("stopped by signal, PADT sent")
		os.Exit(0)
	}()

	discover()

	var (
		lcpID, ipID           byte = 1, 1
		lcpOurAck, lcpPeerAck bool
		ipOurAck, ipPeerAck   bool
		authMode              uint16
		authSent, up          bool
		ourIP                 = net.IPv4zero.To4()
		peerIP                net.IP
		lastLCP, lastIP       time.Time
	)
	mru := []byte{1, 4, 0x05, 0xc8}
	mg := func() []byte {
		m := make([]byte, 6)
		m[0], m[1] = 5, 6
		binary.BigEndian.PutUint32(m[2:], magic)
		return m
	}
	buf := make([]byte, 2048)
	start := time.Now()
	for {
		if !up && time.Since(start) > 40*time.Second {
			die(12, "session did not come up in 40s")
		}
		if !lcpOurAck && time.Since(lastLCP) > 2*time.Second {
			lastLCP = time.Now()
			lcpID++
			sendPPP(lcp, ctl(1, lcpID, append(append([]byte{}, mru...), mg()...)))
		}
		if authSent && lcpOurAck && !ipOurAck && time.Since(lastIP) > 2*time.Second {
			lastIP = time.Now()
			ipID++
			sendPPP(ipcp, ctl(1, ipID, append([]byte{3, 6}, ourIP...)))
		}
		typ, _, p, ok := recv(buf)
		if !ok || typ == 0 || len(p) < 6 {
			continue
		}
		if typ == ethDisc {
			if p[1] == padt && binary.BigEndian.Uint16(p[2:]) == sid {
				die(10, "PADT from server (session dropped)")
			}
			continue
		}
		if typ != ethSess || binary.BigEndian.Uint16(p[2:]) != sid || len(p) < 8 {
			continue
		}
		l := int(binary.BigEndian.Uint16(p[4:]))
		if 6+l > len(p) || l < 2 {
			continue
		}
		proto, d := binary.BigEndian.Uint16(p[6:]), p[8:6+l]
		if len(d) < 4 && proto != 0x0021 {
			continue
		}
		switch proto {
		case lcp:
			code, id, opts := d[0], d[1], d[4:]
			switch code {
			case 1: // peer Configure-Request
				reply := byte(2)
				var nak []byte
				for o := opts; len(o) >= 2 && int(o[1]) <= len(o) && o[1] >= 2; o = o[o[1]:] {
					if o[0] == 3 { // auth protocol
						switch {
						case o[1] == 5 && binary.BigEndian.Uint16(o[2:]) == chap && o[4] == 5:
							authMode = chap
						case o[1] == 4 && binary.BigEndian.Uint16(o[2:]) == pap:
							authMode = pap
						default:
							reply, nak = 3, []byte{3, 5, 0xc2, 0x23, 5}
						}
					}
				}
				if reply == 2 {
					sendPPP(lcp, ctl(2, id, opts))
					lcpPeerAck = true
				} else {
					sendPPP(lcp, ctl(3, id, nak))
				}
			case 2:
				lcpOurAck = true
			case 3, 4: // Nak/Reject of our request: drop MRU, retry
				mru = nil
				lcpID++
				sendPPP(lcp, ctl(1, lcpID, mg()))
			case 5:
				sendPPP(lcp, ctl(6, id, nil))
				die(11, "LCP Terminate-Request from server")
			case 9:
				sendPPP(lcp, ctl(10, id, mg()[2:]))
			}
			if lcpOurAck && lcpPeerAck && authMode == pap && !authSent {
				authSent = true
				b := append(append([]byte{byte(len(*user))}, *user...), byte(len(*pass)))
				sendPPP(pap, ctl(1, 1, append(b, *pass...)))
			}
		case chap:
			switch d[0] {
			case 1:
				vs := int(d[4])
				chal := d[5 : 5+vs]
				h := md5.New()
				h.Write([]byte{d[1]})
				h.Write([]byte(*pass))
				h.Write(chal)
				resp := append([]byte{16}, h.Sum(nil)...)
				sendPPP(chap, ctl(2, d[1], append(resp, *user...)))
			case 3:
				logf("CHAP auth ok")
				authSent = true
			case 4:
				die(1, "CHAP auth failed: %s", d[4:])
			}
		case pap:
			switch d[0] {
			case 2:
				logf("PAP auth ok")
			case 3:
				die(1, "PAP auth failed: %s", d[4:])
			}
		case ipcp:
			code, id, opts := d[0], d[1], d[4:]
			switch code {
			case 1:
				if len(opts) >= 6 && opts[0] == 3 {
					peerIP = net.IP(append([]byte{}, opts[2:6]...))
				}
				sendPPP(ipcp, ctl(2, id, opts))
				ipPeerAck = true
			case 2:
				ipOurAck = true
			case 3: // Nak: take the address the server offers
				if len(opts) >= 6 && opts[0] == 3 {
					ourIP = net.IP(append([]byte{}, opts[2:6]...)).To4()
					ipID++
					lastIP = time.Now()
					sendPPP(ipcp, ctl(1, ipID, append([]byte{3, 6}, ourIP...)))
				}
			case 4:
				die(1, "IPCP rejected by server")
			}
			if ipOurAck && ipPeerAck && !up {
				up = true
				logf("session up ip=%s peer=%s sid=%#x", ourIP, peerIP, sid)
			}
		case 0x0021: // IP data, ignore
		default:
			if proto&0x8000 != 0 && len(d) >= 4 { // NCP we do not run (IPV6CP, CCP): Protocol-Reject
				lcpID++
				sendPPP(lcp, ctl(8, lcpID, append([]byte{byte(proto >> 8), byte(proto)}, d...)))
			}
		}
	}
}
