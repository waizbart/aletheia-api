// Package migrations embeds the SQL migration files so they ship inside the
// binary and can be applied by the golang-migrate runner (see internal/migrate).
package migrations

import "embed"

// FS holds every *.sql migration in this directory, named with the golang-migrate
// {version}_{title}.{up|down}.sql convention.
//
//go:embed *.sql
var FS embed.FS
