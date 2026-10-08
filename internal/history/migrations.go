package history

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
)

const (
	// Format 3 shipped in v0.0.1 and is the oldest schema in repository history.
	oldestSchemaVersion = 3
	schemaVersion       = 6
)

// Keep released scripts unchanged. For each schema or durable data format change, add
// the next numbered script. Increase schemaVersion.
//
//go:embed migrations/*.sql
var migrations embed.FS

func initialize(db *sql.DB) error {
	if err := migrate(db); err != nil {
		return err
	}
	return enableWAL(db)
}

func migrate(db *sql.DB) error {
	ctx := context.Background()
	// Pin the transaction and connection settings to the same connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Serialize version reads, new database creation, and upgrades. A second opener sees
	// the committed version and skips completed migrations.
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin history migration: %w", err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	var version int
	if err = conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("history format %d is newer than this build supports (%d); use a newer BonBon executable; database was not changed", version, schemaVersion)
	}
	if version != 0 && version < oldestSchemaVersion {
		return fmt.Errorf("unsupported history format %d; supported formats are %d through %d; database was not changed", version, oldestSchemaVersion, schemaVersion)
	}
	if version == 0 {
		var objects int
		if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&objects); err != nil {
			return err
		}
		if objects != 0 {
			return errors.New("unrecognized unversioned history database; database was not changed")
		}
	}
	next := version + 1
	if version == 0 {
		next = oldestSchemaVersion
	}
	for ; next <= schemaVersion; next++ {
		name := fmt.Sprintf("migrations/%03d.sql", next)
		script, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read history migration %d: %w", next, err)
		}
		if _, err = conn.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("apply history migration %d (upgrade rolled back): %w", next, err)
		}
		if _, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", next)); err != nil {
			return fmt.Errorf("record history migration %d: %w", next, err)
		}
	}
	if err = checkSchema(ctx, conn); err != nil {
		return fmt.Errorf("validate history schema (upgrade rolled back): %w", err)
	}
	if version != schemaVersion {
		// Check references before committing any schema or version changes.
		rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		invalid := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if invalid {
			return errors.New("history migration found invalid foreign keys; upgrade rolled back")
		}
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit history migration: %w", err)
	}
	return nil
}
