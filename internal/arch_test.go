package internal

import (
	"os/exec"
	"strings"
	"testing"
)

// TestImportRules keeps the package layering of docs/internal/rencana/modularisasi.md.
// Each rule: packages under `from` must not import any package under `deny`.
// Add a rule here when a stage of the plan makes a boundary true; never loosen one to make a change pass.
func TestImportRules(t *testing.T) {
	const mod = "github.com/frand-kod/gobill/internal/"
	rules := []struct {
		from string
		deny []string
	}{
		// domain packages never reach up into the HTTP layer
		{"billing", []string{"web"}},
		{"radius", []string{"web"}},
		{"notify", []string{"web"}},
		{"payment", []string{"web"}},
		// M5: the cores meet billing only through small interfaces (billing.Notifier, radius.UsageRecorder)
		{"radius", []string{"billing", "notify"}},
		{"notify", []string{"billing", "radius"}},
		{"payment", []string{""}},
		{"isolir", []string{"web", "billing", "radius"}},
		{"importer", []string{"web"}},
		// services take plain inputs and never see HTTP
		{"service", []string{"web"}},
		// device drivers and the WireGuard manager stand beside the RADIUS server, not on it
		{"device", []string{"web", "radius"}},
		{"wg", []string{"web", "radius"}},
		// the scheduler is generic: update checks are registered from internal/app
		{"job", []string{"web", "update", "selfupdate"}},
		// services never reach up into the HTTP layer
		// web sub-packages stand beside each other and never import web or a sibling (httpx and view are the shared leaves)
		{"web/httpx", []string{"web"}},
		{"web/view", []string{"web"}},
		// kernel packages import nothing internal
		{"money", []string{""}},
		{"clock", []string{""}},
		{"secret", []string{""}},
	}
	for _, r := range rules {
		out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./"+r.from+"/...").Output()
		if err != nil {
			t.Fatalf("go list %s: %v", r.from, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			for _, imp := range f[1:] {
				for _, d := range r.deny {
					if d == "" && strings.HasPrefix(imp, mod) || d != "" && (imp == mod+d || strings.HasPrefix(imp, mod+d+"/")) {
						t.Errorf("%s imports %s (rule: %s must not import internal/%s)", f[0], imp, r.from, d)
					}
				}
			}
		}
	}
}
