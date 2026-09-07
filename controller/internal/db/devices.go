package db

import "time"

// Device represents an enrolled node.
type Device struct {
	ID           string     `json:"id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	WGPublicKey  string     `json:"wg_public_key"`
	MeshIP       string     `json:"mesh_ip"`
	CertPEM      string     `json:"-"`
	KeyPEM       string     `json:"-"`
	LastSeen     *time.Time `json:"last_seen"`
	LastEndpoint string     `json:"last_endpoint"`
	AgentVersion string     `json:"agent_version"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (d *DB) CreateDevice(dev Device) error {
	_, err := d.conn.Exec(
		`INSERT INTO devices(id, hostname, os, wg_public_key, mesh_ip, cert_pem, key_pem, agent_version, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		dev.ID, dev.Hostname, dev.OS, dev.WGPublicKey, dev.MeshIP, dev.CertPEM, dev.KeyPEM,
		dev.AgentVersion, time.Now().UTC(),
	)
	return err
}

func (d *DB) GetDevice(id string) (*Device, error) {
	return scanDevice(d.conn.QueryRow(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, key_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices WHERE id = ?`, id,
	))
}

func (d *DB) GetDeviceByPubKey(pubKey string) (*Device, error) {
	return scanDevice(d.conn.QueryRow(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, key_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices WHERE wg_public_key = ?`, pubKey,
	))
}

func (d *DB) GetDeviceByHostname(hostname string) (*Device, error) {
	return scanDevice(d.conn.QueryRow(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, key_pem, last_seen, last_endpoint, agent_version, created_at
		 FROM devices WHERE hostname = ?`, hostname,
	))
}

func (d *DB) ListDevices() ([]Device, error) {
	rows, err := d.conn.Query(
		`SELECT id, hostname, os, wg_public_key, mesh_ip, cert_pem, key_pem, last_seen, last_endpoint, agent_version, created_at
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
