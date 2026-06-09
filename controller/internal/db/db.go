package db

import "database/sql"

// DB wraps a SQLite connection and owns all schema migrations.
type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	// TODO: open SQLite, run migrations
	_ = path
	return &DB{}, nil
}

func (d *DB) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
