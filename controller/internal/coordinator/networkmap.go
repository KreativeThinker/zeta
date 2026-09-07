package coordinator

import (
	"log/slog"

	"github.com/kreativethinker/zeta/proto/zetapb"
)

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
				Name:           s.Name,
				Port:           uint32(s.Port),
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
