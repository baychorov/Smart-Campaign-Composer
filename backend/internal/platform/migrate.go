package platform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/baychorov/Smart-Campaign-Composer/backend/migrations"
)

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return withMigrator(pool, func(p *goose.Provider) error {
		if _, err := p.Up(ctx); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		return nil
	})
}

// MigrateReset rolls back every migration, dropping all application tables.
// Used by `seed -reset` and by tests.
func MigrateReset(ctx context.Context, pool *pgxpool.Pool) error {
	return withMigrator(pool, func(p *goose.Provider) error {
		if _, err := p.DownTo(ctx, 0); err != nil {
			return fmt.Errorf("migrate reset: %w", err)
		}
		return nil
	})
}

// withMigrator adapts the pgx pool to database/sql, which goose requires.
func withMigrator(pool *pgxpool.Pool, fn func(*goose.Provider) error) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	return fn(p)
}
