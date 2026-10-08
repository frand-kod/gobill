// Package nuxbill only holds the files embedded into the binary.
package nuxbill

import "embed"

// FS holds templates, static files and language files.
//
//go:embed web lang
var FS embed.FS
