package db

import "github.com/google/uuid"

// Service is a named port a device exposes on the mesh.
type Service struct {
	ID         string   `json:"id"`
	DeviceID   string   `json:"device_id"`
	Name       string   `json:"name"`
	Port       int      `json:"port"`
	TargetAddr string   `json:"target_addr"`
	AllowedPKs []string `json:"allowed_pubkeys"` // WG pubkeys; populated by ListServicesByDevice
}

// ServiceWithDevice enriches a Service with its host device's hostname and mesh IP.
type ServiceWithDevice struct {
	Service
	DeviceHostname string `json:"device_hostname"`
	DeviceMeshIP   string `json:"device_mesh_ip"`
}

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
			`INSERT INTO services(id, device_id, name, port, target_addr) VALUES(?,?,?,?,?)`,
			s.ID, deviceID, s.Name, s.Port, s.TargetAddr,
		); err != nil {
			return err
		}
		for _, pk := range s.AllowedPKs {
			if _, err := tx.Exec(
				`INSERT INTO service_access(service_id, allowed_pubkey) VALUES(?,?)`,
				s.ID, pk,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) ListAllServices() ([]ServiceWithDevice, error) {
	rows, err := d.conn.Query(`
		SELECT s.id, s.device_id, s.name, s.port, s.target_addr,
		       d.hostname, d.mesh_ip
		FROM services s
		JOIN devices d ON d.id = s.device_id
		ORDER BY d.hostname, s.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var svcs []ServiceWithDevice
	for rows.Next() {
		var s ServiceWithDevice
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.Name, &s.Port, &s.TargetAddr,
			&s.DeviceHostname, &s.DeviceMeshIP); err != nil {
			return nil, err
		}
		svcs = append(svcs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range svcs {
		pks, err := d.loadServiceACL(svcs[i].ID)
		if err != nil {
			return nil, err
		}
		svcs[i].AllowedPKs = pks
	}
	return svcs, nil
}

func (d *DB) ListServicesByDevice(deviceID string) ([]Service, error) {
	rows, err := d.conn.Query(
		`SELECT id, device_id, name, port, target_addr FROM services WHERE device_id = ?`, deviceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var svcs []Service
	for rows.Next() {
		var s Service
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.Name, &s.Port, &s.TargetAddr); err != nil {
			return nil, err
		}
		svcs = append(svcs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range svcs {
		pks, err := d.loadServiceACL(svcs[i].ID)
		if err != nil {
			return nil, err
		}
		svcs[i].AllowedPKs = pks
	}
	return svcs, nil
}

// loadServiceACL fetches the allowed pubkeys for a single service.
func (d *DB) loadServiceACL(serviceID string) ([]string, error) {
	rows, err := d.conn.Query(`SELECT allowed_pubkey FROM service_access WHERE service_id = ?`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pks []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		pks = append(pks, pk)
	}
	return pks, rows.Err()
}
