package notify

import (
	"regexp"
	"strings"
	"testing"
)

// Every [[var]] in a built-in template is listed for that template, so the settings chips cover it.
func TestDefaultTemplateVarsListed(t *testing.T) {
	re := regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	for name, tpl := range defaults {
		listed := map[string]bool{}
		for _, v := range Vars[name] {
			listed[v.Name] = true
		}
		for _, m := range re.FindAllStringSubmatch(tpl, -1) {
			if !listed[m[1]] {
				t.Errorf("%s: [[%s]] is in the built-in text but not in its variables", name, m[1])
			}
		}
	}
	for name := range Vars {
		if _, ok := defaults[name]; !ok {
			t.Errorf("variables for %q, which has no built-in text", name)
		}
	}
}

// A blank stored template falls back to the built-in text, so a message is never sent empty.
func TestBlankTemplateUsesDefault(t *testing.T) {
	n := &Notifier{Settings: map[string]string{"notif_expired": " \r\n\t", "notif_welcome_message": "  "}}
	for _, name := range []string{"expired", "welcome_message"} {
		if got := n.template(name); got != defaults[name] {
			t.Errorf("%s: template %q, want the built-in text", name, got)
		}
		if msg := n.fill(n.template(name), map[string]string{"name": "Budi"}); strings.TrimSpace(msg) == "" {
			t.Errorf("%s: an empty message would be sent", name)
		}
	}
}
