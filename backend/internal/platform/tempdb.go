package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateTempDB creates an empty database named prefix_<random> on the
// server of databaseURL and returns a pool connected to it. drop closes the
// pool and removes the database; call it when done. Integration tests
// (pgtest) and make eval use it for an isolated, reproducible dataset.
func CreateTempDB(ctx context.Context, databaseURL, prefix string) (pool *pgxpool.Pool, drop func() error, err error) {
	admin, err := OpenDB(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := prefix + "_" + hex.EncodeToString(b)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("create database: %w", err)
	}

	dropDB := func() error {
		defer admin.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("drop database %s: %w", name, err)
		}
		return nil
	}

	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		_ = dropDB()
		return nil, nil, fmt.Errorf("invalid DATABASE_URL")
	}
	poolCfg.ConnConfig.Database = name
	pool, err = pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		_ = dropDB()
		return nil, nil, fmt.Errorf("connect temp database: %w", err)
	}
	return pool, func() error { pool.Close(); return dropDB() }, nil
}
