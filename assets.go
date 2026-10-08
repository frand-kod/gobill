// Package nuxbill only holds the files embedded into the binary.
package nuxbill

import "embed"

//go:generate sh tools/tailwind.sh

// FS holds templates, static files and language files.
//
//go:embed web lang
var FS embed.FS
