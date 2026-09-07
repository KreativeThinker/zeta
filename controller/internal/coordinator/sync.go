package coordinator

import (
	"log/slog"
	"strings"

	"github.com/kreativethinker/zeta/controller/internal/db"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

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
				if pk, ok := resolveServiceACL(entry, c.db.GetDeviceByHostname); ok {
					svc.AllowedPKs = append(svc.AllowedPKs, pk)
				}
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

// resolveServiceACL resolves one zetafile `access:` entry to a WireGuard
// pubkey to allow. "*" is the wildcard sentinel (any enrolled mesh peer,
// checked at the proxy layer — no hostname resolution needed). Anything else
// is a "user:<hostname>" (or bare hostname) entry resolved via lookup.
func resolveServiceACL(entry string, lookup func(string) (*db.Device, error)) (string, bool) {
	if entry == "*" {
		return "*", true
	}
	hostname := entry
	if h, ok := strings.CutPrefix(entry, "user:"); ok {
		hostname = h
	}
	dev, err := lookup(hostname)
	if err != nil {
		slog.Warn("resolving ACL hostname", "hostname", hostname, "err", err)
		return "", false
	}
	if dev == nil {
		slog.Warn("ACL hostname not found", "hostname", hostname)
		return "", false
	}
	return dev.WGPublicKey, true
}
