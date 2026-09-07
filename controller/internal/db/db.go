package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// DB wraps a SQLite connection.
type DB struct {
	conn *sql.DB
}

// Open opens or creates the SQLite database at path and applies the schema.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite is single-writer

	if _, err := conn.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		return nil, fmt.Errorf("pragma: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if err := migrate(conn); err != nil {
		return nil, fmt.Errorf("migrating db: %w", err)
	}
	return &DB{conn: conn}, nil
}

// migrate applies additive schema changes that CREATE TABLE IF NOT EXISTS cannot handle.
func migrate(conn *sql.DB) error {
	migrations := []string{
		`ALTER TABLE devices ADD COLUMN key_pem TEXT`,
		`ALTER TABLE services ADD COLUMN target_addr TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS service_access (
			service_id     TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
			allowed_pubkey TEXT NOT NULL,
			PRIMARY KEY (service_id, allowed_pubkey)
		)`,
	}
	for _, m := range migrations {
		if _, err := conn.Exec(m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") &&
				!strings.Contains(err.Error(), "already exists") {
				return err
			}
		}
	}
	return nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(row scanner) (*Device, error) {
	var dev Device
	var lastSeen sql.NullTime
	var certPEM sql.NullString
	var keyPEM sql.NullString
	var lastEndpt sql.NullString
	var agentVer sql.NullString
	err := row.Scan(
		&dev.ID, &dev.Hostname, &dev.OS, &dev.WGPublicKey, &dev.MeshIP,
		&certPEM, &keyPEM, &lastSeen, &lastEndpt, &agentVer, &dev.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		dev.LastSeen = &lastSeen.Time
	}
	dev.CertPEM = certPEM.String
	dev.KeyPEM = keyPEM.String
	dev.LastEndpoint = lastEndpt.String
	dev.AgentVersion = agentVer.String
	return &dev, nil
}
