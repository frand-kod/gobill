package public

import (
	"html/template"
	"strings"
	"testing"
)

func TestRenderIsolirCustomEscapesValues(t *testing.T) {
	out := renderIsolirCustom(`[[name]]|[[promos]]|[[qris_image]]|[[qris_merchant]]|[[nope]]|[[voucher_form]]`, isolirView{
		Name: `<a>"x"</a>`, Promos: template.HTML("<b>p</b>"), VoucherForm: "<form>v</form>"})
	// old QRIS variables are no longer filled: they become empty so old custom pages still render
	want := `&lt;a&gt;&#34;x&#34;&lt;/a&gt;|<b>p</b>||||<form>v</form>`
	if out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
}

// [[logout_url]] is the logout address in custom HTML, empty when unset, and it is escaped.
func TestIsolirLogoutURLVariable(t *testing.T) {
	if got := renderIsolirCustom("[[logout_url]]", isolirView{}); got != "" {
		t.Fatalf("unset: %q", got)
	}
	v := isolirView{LogoutURL: isoLogout("http://fourkeys.net")}
	if got := renderIsolirCustom(`<a href="[[logout_url]]">x</a>`, v); got != `<a href="http://fourkeys.net/logout">x</a>` {
		t.Fatalf("set: %q", got)
	}
	if got := isoLogout(`http://x"y`); strings.Contains(renderIsolirCustom("[[logout_url]]", isolirView{LogoutURL: got}), `"`) {
		t.Fatal("quote not escaped")
	}
}

// isoLogout is the logout address for a hotspot address, as the page builds it.
func isoLogout(base string) string {
	return isolirLogoutURL(map[string]string{"isolir_hotspot_url": base + "/"})
}
