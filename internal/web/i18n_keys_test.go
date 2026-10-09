package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	nuxbill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/i18n"
)

// Every literal {{T "..."}} / {{TT "..."}} in the templates must exist in lang/indonesia.json.
func TestTemplateKeysTranslated(t *testing.T) {
	cat, err := i18n.Load(nuxbill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	id := cat["indonesia"]
	re := regexp.MustCompile(`\bTT? "([^"]+)"`)
	nonWord := regexp.MustCompile(`[^A-Za-z0-9]`)
	seen := map[string]bool{}
	fs.WalkDir(nuxbill.FS, "web/templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, _ := fs.ReadFile(nuxbill.FS, p)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			k := m[1]
			if !seen[k] && id[k] == "" && id[nonWord.ReplaceAllString(k, "_")] == "" {
				t.Errorf("%s: %q missing from lang/indonesia.json", p, k)
			}
			seen[k] = true
		}
		return nil
	})
}
