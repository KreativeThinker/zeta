package db

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// DB wraps a SQLite connection.
type DB struct {
	conn *sql.DB
}

// Device represents an enrolled node.
type Device struct {
	ID           string     `json:"id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	WGPublicKey  string     `json:"wg_public_key"`
	MeshIP       string     `json:"mesh_ip"`
	CertPEM      string     `json:"-"`
	LastSeen     *time.Time `json:"last_seen"`
	LastEndpoint string     `json:"last_endpoint"`
	AgentVersion string     `json:"agent_version"`
	CreatedAt    time.Time  `json:"created_at"`
}

// PreauthKey is a one-time or reusable enrollment token.
type PreauthKey struct {
	Key       string     `json:"key"`
	Label     string     `json:"label"`
	Reusable  bool       `json:"reusable"`
	Expiry    time.Time  `json:"expiry"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Service is a named port a device exposes on the mesh.
type Service struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
}

// AuditEntry is a single audit log record.
type AuditEntry struct {
	ID        int64     `json:"id"`
	Event     string    `json:"event"`
	DeviceID  string    `json:"device_id"`
	Metadata  string    `json:"metadata"`
	CreatedAt time.Time `json:"created_at"`
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
	return &DB{conn: conn}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// ── Settings ──────────────────────────────────────────────────────────────────

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.conn.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// ── Devices ───────────────────────────────────────────────────────────────────

func (d *DB) CreateDevice(dev Device) error {
	_, err := d.conn.Exec(
		`INSERT INTO devices(id, hostname, os, wg_public_key, mesh_ip, cert_pem, agent_version, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		dev.ID, dev.Hostname, dev.OS, dev.WGPublicKey, dev.MeshIP, dev.CertPEM,
		dev.AgentVersion, time.Now().UTC(),
	)
	return err
}

func (d *DB) GetDevice(id string) (*Device, error) {
	return scanDevice(d.conn.QueryRow(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices WHERE id = ?`, id,
	))
}

func (d *DB) GetDeviceByPubKey(pubKey string) (*Device, error) {
	return scanDevice(d.conn.QueryRow(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices WHERE wg_public_key = ?`, pubKey,
	))
}

func (d *DB) ListDevices() ([]Device, error) {
	rows, err := d.conn.Query(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, *dev)
	}
	return devices, rows.Err()
}

func (d *DB) UpdateDeviceEndpoint(id, endpoint string) error {
	_, err := d.conn.Exec(`UPDATE devices SET last_endpoint = ? WHERE id = ?`, endpoint, id)
	return err
}

func (d *DB) UpdateDeviceLastSeen(id string) error {
	_, err := d.conn.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, time.Now().UTC(), id)
	return err
}

func (d *DB) DeleteDevice(id string) error {
	_, err := d.conn.Exec(`DELETE FROM devices WHERE id = ?`, id)
	return err
}

// AllocateIP finds the lowest available address in cidr above controllerIP.
func (d *DB) AllocateIP(cidr, controllerIP string) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid cidr %q: %w", cidr, err)
	}

	rows, err := d.conn.Query(`SELECT mesh_ip FROM devices`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	used := map[string]bool{controllerIP: true}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return "", err
		}
		used[ip] = true
	}

	// Walk IPs starting right after the network address.
	ip := cloneIP(network.IP)
	incIP(ip) // skip network address
	incIP(ip) // skip controller IP slot (100.64.0.1 reserved even if not in used map)

	for network.Contains(ip) {
		candidate := ip.String()
		if !used[candidate] {
			return candidate, nil
		}
		ip = cloneIP(ip)
		incIP(ip)
	}
	return "", fmt.Errorf("address pool exhausted in %s", cidr)
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incIP(ip net.IP) {
	// treat as big-endian integer and add 1
	n := new(big.Int).SetBytes(ip)
	n.Add(n, big.NewInt(1))
	b := n.Bytes()
	// pad to original length
	padded := make([]byte, len(ip))
	copy(padded[len(padded)-len(b):], b)
	copy(ip, padded)
}

// ── Preauth Keys ──────────────────────────────────────────────────────────────

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

// ── Services ──────────────────────────────────────────────────────────────────

func (d *DB) UpsertServices(deviceID string, svcs []Service) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM services WHERE device_id = ?`, deviceID); err != nil {
		return err
	}
	for _, s := range svcs {
		if s.ID == "" {
			s.ID = uuid.NewString()
		}
		if _, err := tx.Exec(
			`INSERT INTO services(id, device_id, name, port) VALUES(?,?,?,?)`,
			s.ID, deviceID, s.Name, s.Port,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) ListServicesByDevice(deviceID string) ([]Service, error) {
	rows, err := d.conn.Query(
		`SELECT id, device_id, name, port FROM services WHERE device_id = ?`, deviceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var svcs []Service
	for rows.Next() {
		var s Service
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.Name, &s.Port); err != nil {
			return nil, err
		}
		svcs = append(svcs, s)
	}
	return svcs, rows.Err()
}

// ── Audit ─────────────────────────────────────────────────────────────────────

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

// ── helpers ───────────────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(row scanner) (*Device, error) {
	var dev Device
	var lastSeen    sql.NullTime
	var certPEM     sql.NullString
	var lastEndpt   sql.NullString
	var agentVer    sql.NullString
	err := row.Scan(
		&dev.ID, &dev.Hostname, &dev.OS, &dev.WGPublicKey, &dev.MeshIP,
		&certPEM, &lastSeen, &lastEndpt, &agentVer, &dev.CreatedAt,
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
	dev.CertPEM      = certPEM.String
	dev.LastEndpoint = lastEndpt.String
	dev.AgentVersion = agentVer.String
	return &dev, nil
}
