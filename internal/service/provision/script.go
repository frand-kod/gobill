package provision

import (
	"fmt"
	"net/netip"
	"strings"
)

// Block holds what the RouterOS script of one peer names.
type Block struct {
	Private, ServerPub, Endpoint, Secret, APIPass string
	Port                                          int
	Self, Peer                                    netip.Addr
	Bits                                          int
}

// Names the script owns on the router: it removes them before adding them again, so running it twice
// leaves one copy.
const (
	Iface = "wg-gobill"
	User  = "gobill"
	Group = "gobill-api"
	Tag   = "gobill"
)

// rosQuote makes a RouterOS double-quoted string. RouterOS expands $ and $[...] inside quotes on /import,
// so a value with `$[/user add ...]` would run commands as the importing user. Backslash, quote and $ are
// escaped; a control character has no safe form and is refused.
func rosQuote(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			return "", fmt.Errorf("provision: control character %#x in a RouterOS value", r)
		case r == '\\' || r == '"' || r == '$':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String(), nil
}

// Script is the RouterOS 7 script of one router, served once through /provision/<token>. It stops on
// RouterOS 6, removes what an earlier run made, then adds the tunnel, NTP (a router whose clock restarts
// at its factory date is refused by WireGuard), RADIUS from the tunnel address, the firewall holes for
// CoA and the API from gobill's tunnel address only, and the API user in a group of its own. The text
// depends only on the router and its stored keys.
func (b Block) Script() (string, error) {
	ep := b.Endpoint
	if ep == "" {
		ep = "<DOMAIN-OR-IP-GOBILL>"
	}
	var qerr error
	q := func(s string) string {
		v, err := rosQuote(s)
		if err != nil && qerr == nil {
			qerr = err
		}
		return v
	}
	var w strings.Builder
	w.WriteString("# gobill router provisioning. Safe to run again.\n")
	w.WriteString(":local v [/system resource get version]\n")
	w.WriteString(":if ([:tonum [:pick $v 0 [:find $v \".\"]]] < 7) do={ :error \"gobill: RouterOS 7 is required, this router runs $v. RouterOS 6 has no WireGuard: use L2TP/IPsec or SSTP, see the gobill docs\" }\n")
	fmt.Fprintf(&w, "/user remove [find name=%s]\n", User)
	fmt.Fprintf(&w, "/user group remove [find name=%s]\n", Group)
	fmt.Fprintf(&w, "/radius remove [find comment=%s]\n", Tag)
	fmt.Fprintf(&w, "/radius remove [find address=%s]\n", b.Self)
	fmt.Fprintf(&w, "/ip firewall filter remove [find comment=%s]\n", Tag)
	fmt.Fprintf(&w, "/ip address remove [find interface=%s]\n", Iface)
	fmt.Fprintf(&w, "/interface wireguard peers remove [find interface=%s]\n", Iface)
	fmt.Fprintf(&w, "/interface wireguard remove [find name=%s]\n", Iface)
	fmt.Fprintf(&w, "/interface wireguard add name=%s private-key=%s mtu=1420\n", Iface, q(b.Private))
	fmt.Fprintf(&w, "/interface wireguard peers add interface=%s public-key=%s endpoint-address=%s endpoint-port=%d allowed-address=%s/32 persistent-keepalive=25s\n", Iface, q(b.ServerPub), ep, b.Port, b.Self)
	fmt.Fprintf(&w, "/ip address add address=%s/%d interface=%s\n", b.Peer, b.Bits, Iface)
	w.WriteString("/system ntp client set enabled=yes servers=pool.ntp.org\n")
	fmt.Fprintf(&w, "/radius add service=hotspot,ppp address=%s secret=%s authentication-port=1812 accounting-port=1813 timeout=1s src-address=%s comment=%s\n", b.Self, q(b.Secret), b.Peer, Tag)
	w.WriteString("/radius incoming set accept=yes\n")
	// place-before=0 fails with "no such item" on an empty table and, after the cleanup above, on a stale index;
	// the first rule is looked up by id, and a plain add is used when the table is empty.
	const first = "[:pick [/ip firewall filter find] 0]"
	for _, r := range []string{"protocol=udp dst-port=3799", "protocol=tcp dst-port=8728"} {
		add := fmt.Sprintf("/ip firewall filter add chain=input in-interface=%s src-address=%s %s action=accept", Iface, b.Self, r)
		fmt.Fprintf(&w, ":if ([:len [/ip firewall filter find]] = 0) do={ %s comment=%s } else={ %s place-before=%s comment=%s }\n", add, Tag, add, first, Tag)
	}
	fmt.Fprintf(&w, "/user group add name=%s policy=api,read,write,test\n", Group)
	fmt.Fprintf(&w, "/user add name=%s group=%s password=%s address=%s/32\n", User, Group, q(b.APIPass), b.Self)
	fmt.Fprintf(&w, "/ip service set api address=%s/32 disabled=no\n", b.Self)
	w.WriteString("/file remove [find name=gobill.rsc]\n")
	if qerr != nil {
		return "", qerr
	}
	return w.String(), nil
}
