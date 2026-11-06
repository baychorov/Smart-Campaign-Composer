// Package migrations embeds the SQL migration files into the binary.
package migrations

import "embed"

// FS holds every *.sql file in this directory, read at compile time.
//
//go:embed *.sql
var FS embed.FS
