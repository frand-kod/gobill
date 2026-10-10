// Package nuxbill only holds the files embedded into the binary.
package nuxbill

import "embed"

//go:generate sh tools/tailwind.sh

// FS holds templates, static files and language files.
//
//go:embed web lang
var FS embed.FS

// Docs holds the operator guides shown under "Panduan". The list is explicit on purpose: a glob
// could pick up docs/*.sql (real customer data) or the internal audit notes.
//
//go:embed docs/README.md docs/instalasi.md docs/konfigurasi.md docs/mikrotik.md docs/freeradius-rest.md docs/migrasi-phpnuxbill.md docs/keamanan.md docs/monitoring.md
var Docs embed.FS
