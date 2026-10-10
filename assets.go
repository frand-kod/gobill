// Package nuxbill only holds the files embedded into the binary.
package nuxbill

import "embed"

//go:generate sh tools/tailwind.sh

// FS holds templates, static files and language files.
//
//go:embed web lang
var FS embed.FS

// Docs holds the operator guides shown under "Panduan", in Indonesian (docs/id) and English
// (docs/en). The list is explicit on purpose: a glob could pick up docs/*.sql (real customer data)
// or the internal notes in docs/internal.
//
//go:embed docs/id/README.md docs/id/installation.md docs/id/upgrade.md docs/id/migration-phpnuxbill.md docs/id/configuration.md docs/id/integrations.md docs/id/security.md docs/id/mikrotik.md docs/id/freeradius-rest.md docs/id/monitoring.md docs/id/backup-restore.md
//go:embed docs/en/README.md docs/en/installation.md docs/en/upgrade.md docs/en/migration-phpnuxbill.md docs/en/configuration.md docs/en/integrations.md docs/en/security.md docs/en/mikrotik.md docs/en/freeradius-rest.md docs/en/monitoring.md docs/en/backup-restore.md
var Docs embed.FS
