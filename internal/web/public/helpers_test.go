package public

import (
	"testing"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/i18n"
	"github.com/frand-kod/gobill/internal/web/core"
)

const isoQRIS = "00020101021126520016ID.CO.CONTOH.WWW01129360000000010212ID10000000015204549953033605802ID5911CONTOH QRIS6006CONTOH9954XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX63048687"

// testHandlers is a Handlers with the real catalog and the default language (indonesia), enough for T.
func testHandlers(t *testing.T) *Handlers {
	t.Helper()
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Catalog: cat}
	c.Lang.Store("indonesia")
	return New(c)
}
