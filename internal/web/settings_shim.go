package web

import (
	"context"

	"github.com/frand-kod/gobill/internal/service/restore"
)

// SweepUploads is the job body that removes abandoned import and restore uploads, see web/admin/settings.
func (s *Server) SweepUploads(ctx context.Context) error { return s.settings.SweepUploads(ctx) }

// KeyFingerprint is for the backup job, which stores it in the backup.
func KeyFingerprint(key []byte) string { return restore.KeyFingerprint(key) }
