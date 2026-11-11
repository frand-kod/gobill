package provision

import (
	"net/netip"
	"strings"
	"testing"
)

// The script is the same for the same router and removes what it adds before adding it, so a second run
// ends in the same state; RouterOS 6 stops before anything changes.
func TestProvisionScriptShape(t *testing.T) {
	b := Block{Private: "PRIV", ServerPub: "PUB", Endpoint: "vpn.example.com", Secret: "SEC", APIPass: "PW", Port: 51820,
		Self: netip.MustParseAddr("10.99.0.1"), Peer: netip.MustParseAddr("10.99.0.2"), Bits: 24}
	sc, err := b.Script()
	if err != nil {
		t.Fatal(err)
	}
	if sc2, _ := b.Script(); sc != sc2 {
		t.Fatal("script changes between calls")
	}
	idx := func(sub string) int {
		i := strings.Index(sc, sub)
		if i < 0 {
			t.Fatalf("script lacks %q", sub)
		}
		return i
	}
	if v, first := idx("RouterOS 7 is required"), idx("/user remove"); v > first || !strings.Contains(sc[:v], ":error") || !strings.Contains(sc[v:], "L2TP/IPsec or SSTP") {
		t.Error("the version check must stop the script before any change")
	}
	for _, pair := range [][2]string{
		{"/interface wireguard remove [find name=wg-gobill]", "/interface wireguard add name=wg-gobill"},
		{"/radius remove [find comment=gobill]", "/radius add "},
		{"/user remove [find name=gobill]", "/user add name=gobill"},
		{"/user group remove [find name=gobill-api]", "/user group add name=gobill-api"},
		{"/ip firewall filter remove [find comment=gobill]", "/ip firewall filter add "},
	} {
		if idx(pair[0]) > idx(pair[1]) {
			t.Errorf("%q must come before %q", pair[0], pair[1])
		}
	}
	for _, want := range []string{"place-before=[:pick [/ip firewall filter find] 0]", "/ip service set api address=10.99.0.1/32", "group=gobill-api", "address=10.99.0.1/32",
		"/file remove [find name=gobill.rsc]"} {
		if !strings.Contains(sc, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Count(sc, "place-before=") != 2 || strings.Contains(sc, "place-before=0") {
		t.Error("both firewall rules go first, by id, never by index 0")
	}
	// An empty filter table has no first rule: each add with place-before is the else of an emptiness check, a plain add is the then.
	for _, ln := range strings.Split(sc, "\n") {
		if strings.Contains(ln, "place-before=") && (!strings.HasPrefix(ln, `:if ([:len [/ip firewall filter find]] = 0) do={ /ip firewall filter add `) || !strings.Contains(ln, "} else={ /ip firewall filter add ")) {
			t.Errorf("unguarded place-before: %s", ln)
		}
	}
}

func TestRosQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      `"plain"`,
		"":           `""`,
		`a"b`:        `"a\"b"`,
		`a\b`:        `"a\\b"`,
		`a$b`:        `"a\$b"`,
		`x$[/y]`:     `"x\$[/y]"`,
		"päss wörd!": `"päss wörd!"`,
	} {
		if got, err := rosQuote(in); err != nil || got != want {
			t.Errorf("rosQuote(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"a\nb", "a\rb", "a\x00b", "a\tb", "a\x7fb", "\x1b[0m"} {
		if _, err := rosQuote(bad); err == nil {
			t.Errorf("rosQuote(%q) accepted a control character", bad)
		}
	}
}

// A NAS secret or API password with $[...] must not be evaluated by RouterOS on /import.
func TestScriptInjection(t *testing.T) {
	evil := "aaaaaaaaaaaaaaaa$[/user add name=x group=full]"
	b := Block{Private: "PRIV", ServerPub: "PUB", Secret: evil, APIPass: evil, Port: 51820,
		Self: netip.MustParseAddr("10.99.0.1"), Peer: netip.MustParseAddr("10.99.0.2"), Bits: 24}
	sc, err := b.Script()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sc, `\$[/user add name=x group=full]`) != 2 {
		t.Errorf("both values must be escaped:\n%s", sc)
	}
	if strings.Contains(strings.ReplaceAll(sc, `\$[`, ""), "$[/user add") {
		t.Error("unescaped $[ in the script")
	}
	b.Secret = "a\nb"
	if _, err := b.Script(); err == nil {
		t.Error("a control character must fail the script")
	}
}
