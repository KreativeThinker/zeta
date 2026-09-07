package coordinator

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kreativethinker/zeta/controller/internal/db"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

// Enroll validates a preauth key, allocates a mesh IP, issues a device cert,
// persists the device, and returns its NodeConfig.
func (c *Coordinator) Enroll(req *zetapb.RegisterRequest) (*zetapb.NodeConfig, error) {
	if req.PreauthKey == "" {
		return nil, fmt.Errorf("preauth_key required")
	}

	k, err := c.db.GetPreauthKey(req.PreauthKey)
	if err != nil {
		return nil, fmt.Errorf("looking up preauth key: %w", err)
	}
	if k == nil {
		return nil, fmt.Errorf("invalid preauth key")
	}
	if time.Now().After(k.Expiry) {
		return nil, fmt.Errorf("preauth key expired")
	}
	if k.UsedAt != nil && !k.Reusable {
		return nil, fmt.Errorf("preauth key already used")
	}

	// Idempotent re-enrollment: if this pubkey is already registered, return existing config.
	existing, err := c.db.GetDeviceByPubKey(req.WgPublicKey)
	if err != nil {
		return nil, fmt.Errorf("checking existing device: %w", err)
	}
	if existing != nil {
		// Hostname must still match — reject if someone tries to reuse a key under a new name.
		if existing.Hostname != req.Hostname {
			return nil, fmt.Errorf("public key already registered under hostname %q", existing.Hostname)
		}
		return &zetapb.NodeConfig{
			NodeId:  existing.ID,
			MeshIp:  existing.MeshIP,
			Domain:  fmt.Sprintf("%s.%s", existing.Hostname, c.cfg.Mesh.Domain),
			CertPem: []byte(existing.CertPEM),
			CaPem:   c.ca.CertPEM(),
			KeyPem:  []byte(existing.KeyPEM),
		}, nil
	}

	// Reject if the hostname is taken by a different key — prevents ambiguous user: references.
	byHostname, err := c.db.GetDeviceByHostname(req.Hostname)
	if err != nil {
		return nil, fmt.Errorf("checking hostname: %w", err)
	}
	if byHostname != nil && byHostname.WGPublicKey != req.WgPublicKey {
		return nil, fmt.Errorf("hostname %q is already registered by a different device", req.Hostname)
	}

	meshIP, err := c.db.AllocateIP(c.cfg.Mesh.CIDR, c.cfg.Mesh.ControllerIP)
	if err != nil {
		return nil, fmt.Errorf("allocating IP: %w", err)
	}

	nodeID := uuid.NewString()
	certPEM, keyPEM, err := c.ca.IssueDeviceCert(nodeID, meshIP, req.Hostname, c.cfg.Mesh.Domain)
	if err != nil {
		return nil, fmt.Errorf("issuing device cert: %w", err)
	}

	dev := db.Device{
		ID:           nodeID,
		Hostname:     req.Hostname,
		OS:           req.Os,
		WGPublicKey:  req.WgPublicKey,
		MeshIP:       meshIP,
		CertPEM:      string(certPEM),
		KeyPEM:       string(keyPEM),
		AgentVersion: req.AgentVersion,
	}
	if err := c.db.CreateDevice(dev); err != nil {
		return nil, fmt.Errorf("persisting device: %w", err)
	}
	if err := c.db.ConsumePreauthKey(req.PreauthKey); err != nil {
		slog.Warn("failed to consume preauth key", "err", err)
	}
	if err := c.db.AppendAudit("device.enrolled", nodeID, map[string]string{
		"hostname": req.Hostname, "mesh_ip": meshIP, "os": req.Os,
	}); err != nil {
		slog.Warn("audit log failed", "err", err)
	}

	go c.NotifyAll()

	return &zetapb.NodeConfig{
		NodeId:  nodeID,
		MeshIp:  meshIP,
		Domain:  fmt.Sprintf("%s.%s", req.Hostname, c.cfg.Mesh.Domain),
		CertPem: certPEM,
		CaPem:   c.ca.CertPEM(),
		KeyPem:  keyPEM,
	}, nil
}
