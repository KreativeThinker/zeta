package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kreativethinker/zeta/proto/zetapb"
	"github.com/kreativethinker/zeta/controller/internal/ca"
	"github.com/kreativethinker/zeta/controller/internal/config"
	"github.com/kreativethinker/zeta/controller/internal/db"
)

// Coordinator is the central state manager. It owns the live peer registry and
// fans out NetworkMap updates to all connected Sync streams.
type Coordinator struct {
	db  *db.DB
	ca  *ca.CA
	cfg *config.Config

	mu      sync.RWMutex
	streams map[string]chan *zetapb.SyncResponse // nodeID → buffered send channel
	online  map[string]bool
}

func New(database *db.DB, authority *ca.CA, cfg *config.Config) *Coordinator {
	return &Coordinator{
		db:      database,
		ca:      authority,
		cfg:     cfg,
		streams: make(map[string]chan *zetapb.SyncResponse),
		online:  make(map[string]bool),
	}
}

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

// BuildNetworkMap constructs a full NetworkMap from DB state + live online flags.
func (c *Coordinator) BuildNetworkMap() (*zetapb.NetworkMap, error) {
	devices, err := c.db.ListDevices()
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	onlineSnapshot := make(map[string]bool, len(c.online))
	for k, v := range c.online {
		onlineSnapshot[k] = v
	}
	c.mu.RUnlock()

	peers := make([]*zetapb.Peer, 0, len(devices))
	for _, dev := range devices {
		svcs, err := c.db.ListServicesByDevice(dev.ID)
		if err != nil {
			slog.Warn("listing services", "device_id", dev.ID, "err", err)
		}
		pbSvcs := make([]*zetapb.Service, 0, len(svcs))
		for _, s := range svcs {
			pbSvcs = append(pbSvcs, &zetapb.Service{
				Name:          s.Name,
				Port:          uint32(s.Port),
				AllowedPubkeys: s.AllowedPKs,
			})
		}
		peers = append(peers, &zetapb.Peer{
			NodeId:      dev.ID,
			WgPublicKey: dev.WGPublicKey,
			MeshIp:      dev.MeshIP,
			Hostname:    dev.Hostname,
			Endpoint:    dev.LastEndpoint,
			Services:    pbSvcs,
			Online:      onlineSnapshot[dev.ID],
		})
	}

	return &zetapb.NetworkMap{
		Peers: peers,
		Dns: &zetapb.DNSConfig{
			MeshDomain: c.cfg.Mesh.Domain,
			DnsServer:  c.cfg.Mesh.ControllerIP,
		},
		RelayMap: &zetapb.RelayMap{},
	}, nil
}

// RegisterStream adds a node's send channel and immediately pushes the current NetworkMap.
func (c *Coordinator) RegisterStream(nodeID string, ch chan *zetapb.SyncResponse) {
	c.mu.Lock()
	c.streams[nodeID] = ch
	c.online[nodeID] = true
	c.mu.Unlock()

	nm, err := c.BuildNetworkMap()
	if err != nil {
		slog.Error("building network map for new peer", "err", err)
		return
	}
	select {
	case ch <- &zetapb.SyncResponse{Payload: &zetapb.SyncResponse_NetworkMap{NetworkMap: nm}}:
	default:
	}

	// Notify others that a new peer came online.
	go c.notifyExcept(nodeID)
}

// UnregisterStream removes a node's stream and notifies remaining peers.
func (c *Coordinator) UnregisterStream(nodeID string) {
	c.mu.Lock()
	delete(c.streams, nodeID)
	delete(c.online, nodeID)
	c.mu.Unlock()

	if err := c.db.UpdateDeviceLastSeen(nodeID); err != nil {
		slog.Warn("updating last_seen", "err", err)
	}
	go c.NotifyAll()
}

// NotifyAll pushes a fresh NetworkMap to every connected stream (non-blocking).
func (c *Coordinator) NotifyAll() {
	nm, err := c.BuildNetworkMap()
	if err != nil {
		slog.Error("building network map for notify", "err", err)
		return
	}
	msg := &zetapb.SyncResponse{Payload: &zetapb.SyncResponse_NetworkMap{NetworkMap: nm}}

	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, ch := range c.streams {
		select {
		case ch <- msg:
		default:
			slog.Debug("dropping network map update (channel full)", "node_id", id)
		}
	}
}

func (c *Coordinator) notifyExcept(excludeID string) {
	nm, err := c.BuildNetworkMap()
	if err != nil {
		return
	}
	msg := &zetapb.SyncResponse{Payload: &zetapb.SyncResponse_NetworkMap{NetworkMap: nm}}

	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, ch := range c.streams {
		if id == excludeID {
			continue
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

// HandleSyncUpdate processes an inbound update from a connected agent.
func (c *Coordinator) HandleSyncUpdate(nodeID string, upd *zetapb.SyncUpdate) error {
	switch p := upd.Payload.(type) {
	case *zetapb.SyncUpdate_Endpoint:
		if err := c.db.UpdateDeviceEndpoint(nodeID, p.Endpoint.Endpoint); err != nil {
			return err
		}
		go c.NotifyAll()

	case *zetapb.SyncUpdate_Services:
		svcs := make([]db.Service, 0, len(p.Services.Services))
		for _, decl := range p.Services.Services {
			svc := db.Service{
				Name:       decl.Name,
				Port:       int(decl.Port),
				TargetAddr: decl.TargetAddr,
			}
			for _, entry := range decl.AllowedHostnames {
				hostname := entry
				// Strip "user:" prefix.
				if h, ok := strings.CutPrefix(entry, "user:"); ok {
					hostname = h
				}
				dev, err := c.db.GetDeviceByHostname(hostname)
				if err != nil {
					slog.Warn("resolving ACL hostname", "hostname", hostname, "err", err)
					continue
				}
				if dev == nil {
					slog.Warn("ACL hostname not found", "hostname", hostname)
					continue
				}
				svc.AllowedPKs = append(svc.AllowedPKs, dev.WGPublicKey)
			}
			svcs = append(svcs, svc)
		}
		if err := c.db.UpsertServices(nodeID, svcs); err != nil {
			return err
		}
		go c.NotifyAll()

	case *zetapb.SyncUpdate_Ping:
		if err := c.db.UpdateDeviceLastSeen(nodeID); err != nil {
			slog.Warn("updating last_seen on ping", "err", err)
		}
	}
	return nil
}

// IsOnline reports whether a node currently has an active Sync stream.
func (c *Coordinator) IsOnline(nodeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.online[nodeID]
}

// GeneratePreauthKey creates and persists a new preauth key.
func (c *Coordinator) GeneratePreauthKey(label string, ttl time.Duration, reusable bool) (*db.PreauthKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	k := db.PreauthKey{
		Key:      hex.EncodeToString(raw),
		Label:    label,
		Reusable: reusable,
		Expiry:   time.Now().Add(ttl),
	}
	if err := c.db.CreatePreauthKey(k); err != nil {
		return nil, err
	}
	return &k, nil
}
