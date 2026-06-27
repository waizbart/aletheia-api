// Package migrate applies the embedded SQL migrations against a database using
// golang-migrate. Unlike the previous docker-entrypoint-initdb.d approach (which
// only runs on a fresh volume), this runs on every startup and brings an
// existing database up to the latest schema version.
package migrate

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/waizbart/aletheia-api/migrations"
)

// Run applies all pending up migrations. It is idempotent: when the database is
// already at the latest version it returns nil (migrate.ErrNoChange is swallowed).
// databaseURL must be a postgres:// DSN.
func Run(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate: load embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	// Release the source; the database connection is closed by the caller's pool.
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
