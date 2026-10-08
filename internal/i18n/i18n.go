// Package i18n reads the PHPNuxBill language files (lang/*.json) unchanged.
package i18n

import (
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	spaces  = regexp.MustCompile(`\s+`)
	nonWord = regexp.MustCompile(`[^A-Za-z0-9]`)
)

// Catalog maps language name (file name without .json) to its key → text table.
type Catalog map[string]map[string]string

// Load reads every *.json file in dir.
func Load(fsys fs.FS, dir string) (Catalog, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	c := Catalog{}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		c[strings.TrimSuffix(path.Base(f), ".json")] = m
	}
	return c, nil
}

// Languages returns the language names, sorted.
func (c Catalog) Languages() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// T translates text the same way Lang::T does in PHPNuxBill: exact key first,
// then the key with every non-alphanumeric char replaced by "_".
// Unknown text is returned as is.
func (c Catalog) T(lang, text string) string {
	m := c[lang]
	text = spaces.ReplaceAllString(text, " ")
	if v := m[text]; v != "" {
		return v
	}
	if v := m[nonWord.ReplaceAllString(text, "_")]; v != "" {
		return v
	}
	return text
}
