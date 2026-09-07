package db

import (
	"encoding/json"
	"time"
)

// AuditEntry is a single audit log record.
type AuditEntry struct {
	ID        int64     `json:"id"`
	Event     string    `json:"event"`
	DeviceID  string    `json:"device_id"`
	Metadata  string    `json:"metadata"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *DB) AppendAudit(event, deviceID string, meta any) error {
	var metaStr string
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		metaStr = string(b)
	}
	_, err := d.conn.Exec(
		`INSERT INTO audit_log(event, device_id, metadata, created_at) VALUES(?,?,?,?)`,
		event, deviceID, metaStr, time.Now().UTC(),
	)
	return err
}

func (d *DB) ListAudit(limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.conn.Query(
		`SELECT id, event, COALESCE(device_id,''), COALESCE(metadata,''), created_at
		 FROM audit_log ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Event, &e.DeviceID, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
