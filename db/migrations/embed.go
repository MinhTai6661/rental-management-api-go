// Package migrations embeds the goose migration files so the binary can migrate any database it can reach.
package migrations

import "embed"

// FS holds the *.sql migrations, in version order.
//
//go:embed *.sql
var FS embed.FS
