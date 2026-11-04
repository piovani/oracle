package quote

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"gorm.io/gorm"
)

// migrationFiles are the schema changes, applied in file name order: 0001_name.sql, 0002_name.sql...
// To change the schema add a new file; never edit one that already ran anywhere.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLock keeps two processes from migrating at the same time (pg_advisory_xact_lock key).
const migrationLock = 7265001

// Migrate brings the schema up to date: it runs every migration not yet recorded in
// schema_migrations, all in one transaction. The files are idempotent, so a database that was
// created before migrations existed is adopted as it is.
func (p *Postgres) Migrate(ctx context.Context) error {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	return p.quiet(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationLock).Error; err != nil {
			return err
		}
		const create = "CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"
		if err := tx.Exec(create).Error; err != nil {
			return err
		}
		var applied []string
		if err := tx.Raw("SELECT version FROM schema_migrations").Scan(&applied).Error; err != nil {
			return err
		}
		for _, file := range files {
			version := strings.TrimSuffix(path.Base(file), ".sql")
			if slices.Contains(applied, version) {
				continue
			}
			if err := apply(tx, file, version); err != nil {
				return err
			}
		}
		return nil
	})
}

// apply runs one migration file and records it.
func apply(tx *gorm.DB, file, version string) error {
	script, err := migrationFiles.ReadFile(file)
	if err != nil {
		return err
	}
	if err := tx.Exec(string(script)).Error; err != nil {
		return fmt.Errorf("migration %s: %w", version, err)
	}
	return tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version).Error
}
