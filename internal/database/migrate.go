// Package database holds the PostgreSQL store and the migration runner.
//
// The runner owns one bookkeeping table, schema_migrations. It applies every
// file from db/migrations in filename order, exactly once, each file inside its
// own transaction, and records the version, name and SHA-256 checksum only
// after the whole file succeeded.
//
// Idempotency is decided by the ledger, not by IF NOT EXISTS in the SQL: the
// ledger knows whether a file already ran, so a half-applied schema cannot be
// silently skipped, and an edited file is rejected instead of re-applied.
package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/jackc/pgx/v5"
)

// LedgerTable is the bookkeeping table. The name is part of the schema contract
// since migration files and operators inspect it.
const LedgerTable = "schema_migrations"

// advisoryLockKey serializes concurrent migrators in one database. Every
// process that calls Migrate takes the same session-level lock, so a second
// migrator waits instead of racing the first one.
const advisoryLockKey int64 = 0x6572726b // "errk"

const ledgerSetup = `create table if not exists ` + LedgerTable + ` (
	version text primary key,
	name text not null,
	checksum text not null,
	applied_at timestamptz not null default now()
)`

var migrationFilePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one file from db/migrations. Checksum is the hex SHA-256 of the
// raw file bytes, so changing even a comment makes the ledger disagree.
type Migration struct {
	Version  string
	Name     string
	Checksum string
	SQL      string
}

// LoadMigrations reads every NNNN_name.sql file from dir, ordered by version.
// A duplicate version is an error: two files would otherwise run in an
// arbitrary order.
func LoadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %s: %w", dir, err)
	}

	migrations := make([]Migration, 0, len(entries))
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, name := match[1], match[2]
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("migrations %s and %s share version %s", previous, entry.Name(), version)
		}
		seen[version] = entry.Name()

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", path, err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			Checksum: hex.EncodeToString(sum[:]),
			SQL:      string(content),
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// Migrate applies the migrations in dir that LedgerTable does not record yet
// and returns the versions it applied, in order. A second call on an unchanged
// directory returns an empty slice. A recorded migration whose file changed
// fails with a checksum mismatch; a recorded migration whose file disappeared
// fails as well, because the schema on disk would no longer match the ledger.
func Migrate(ctx context.Context, conn *pgx.Conn, dir string) ([]string, error) {
	migrations, err := LoadMigrations(dir)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return nil, fmt.Errorf("acquire the migration lock: %w", err)
	}
	defer func() {
		// Unlock even when ctx is already cancelled, otherwise the lock would
		// stay held until the connection closes.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if _, err := conn.Exec(ctx, ledgerSetup); err != nil {
		return nil, fmt.Errorf("create the migration ledger: %w", err)
	}

	recorded, err := readLedger(ctx, conn)
	if err != nil {
		return nil, err
	}

	byVersion := make(map[string]Migration, len(migrations))
	for _, migration := range migrations {
		byVersion[migration.Version] = migration
	}
	for version, checksum := range recorded {
		migration, ok := byVersion[version]
		if !ok {
			return nil, fmt.Errorf("migration %s is recorded as applied but has no file in %s", version, dir)
		}
		if migration.Checksum != checksum {
			return nil, fmt.Errorf(
				"migration %s (%s): checksum mismatch: the ledger records %s, the file is %s; an applied migration must not be edited, add a new migration instead",
				version, migration.Name, checksum, migration.Checksum,
			)
		}
	}

	var applied []string
	for _, migration := range migrations {
		if _, ok := recorded[migration.Version]; ok {
			continue
		}
		if err := applyOne(ctx, conn, migration); err != nil {
			return applied, fmt.Errorf("migration %s (%s): %w", migration.Version, migration.Name, err)
		}
		applied = append(applied, migration.Version)
	}
	return applied, nil
}

func readLedger(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, "select version, checksum from "+LedgerTable)
	if err != nil {
		return nil, fmt.Errorf("read the migration ledger: %w", err)
	}
	defer rows.Close()

	recorded := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("read the migration ledger: %w", err)
		}
		recorded[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the migration ledger: %w", err)
	}
	return recorded, nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, migration Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		"insert into "+LedgerTable+" (version, name, checksum) values ($1, $2, $3)",
		migration.Version, migration.Name, migration.Checksum,
	); err != nil {
		return fmt.Errorf("record the ledger row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
