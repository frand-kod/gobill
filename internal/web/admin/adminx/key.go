package adminx

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/service/restore"
	"github.com/frand-kod/gobill/internal/web/core"
)

// KeyWarning is the SuperAdmin warning shown until the key is marked saved for the current
// fingerprint. A new key (new fingerprint) shows it again. Empty for other roles.
func (s *Base) KeyWarning(r *http.Request) string {
	if a := core.AdminFrom(r); a == nil || a.Role != "SuperAdmin" {
		return ""
	}
	st, err := s.LoadSettings(r.Context())
	fp := restore.KeyFingerprint(s.SecretKey)
	if err != nil || st["key_saved_fp"] == fp {
		return ""
	}
	return s.T("The encryption key is not saved outside this server and the backup mirror does not copy it. Without it a restored backup cannot open router passwords and API keys. Open Settings > System & Security > Encryption key. Fingerprint: ") + fp
}
