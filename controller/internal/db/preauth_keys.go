package db

import (
	"database/sql"
	"time"
)

// PreauthKey is a one-time or reusable enrollment token.
type PreauthKey struct {
	Key       string     `json:"key"`
	Label     string     `json:"label"`
	Reusable  bool       `json:"reusable"`
	Expiry    time.Time  `json:"expiry"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func (d *DB) CreatePreauthKey(k PreauthKey) error {
	_, err := d.conn.Exec(
		`INSERT INTO preauth_keys(key, label, reusable, expiry, created_at) VALUES(?,?,?,?,?)`,
		k.Key, k.Label, k.Reusable, k.Expiry.UTC(), time.Now().UTC(),
	)
	return err
}

func (d *DB) GetPreauthKey(key string) (*PreauthKey, error) {
	row := d.conn.QueryRow(
		`SELECT key, label, reusable, expiry, used_at, created_at FROM preauth_keys WHERE key = ?`, key,
	)
	var k PreauthKey
	var usedAt sql.NullTime
	err := row.Scan(&k.Key, &k.Label, &k.Reusable, &k.Expiry, &usedAt, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		k.UsedAt = &usedAt.Time
	}
	return &k, nil
}

// ConsumePreauthKey marks used_at and deletes the key if not reusable.
func (d *DB) ConsumePreauthKey(key string) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC()
	if _, err := tx.Exec(`UPDATE preauth_keys SET used_at = ? WHERE key = ?`, now, key); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM preauth_keys WHERE key = ? AND reusable = 0`, key); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ListPreauthKeys() ([]PreauthKey, error) {
	rows, err := d.conn.Query(
		`SELECT key, label, reusable, expiry, used_at, created_at FROM preauth_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []PreauthKey
	for rows.Next() {
		var k PreauthKey
		var usedAt sql.NullTime
		if err := rows.Scan(&k.Key, &k.Label, &k.Reusable, &k.Expiry, &usedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		if usedAt.Valid {
			k.UsedAt = &usedAt.Time
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (d *DB) DeletePreauthKey(key string) error {
	_, err := d.conn.Exec(`DELETE FROM preauth_keys WHERE key = ?`, key)
	return err
}
