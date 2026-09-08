package main

import (
	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

// UpdateDockerServices replaces the set of services discovered via Docker
// Compose labels and re-announces/re-applies firewall rules, mirroring
// UpdateZetafile.
func (a *Agent) UpdateDockerServices(svcs []config.ZetaService) {
	a.dockerSvcs.Store(&svcs)
	a.mu.Lock()
	ch := a.send
	a.mu.Unlock()
	if ch != nil {
		a.announceServices(ch)
	}
	a.applyFirewall()
}

// effectiveServices returns the current set of Docker-label-discovered
// services. There is no manually-declared/bare-metal path anymore — every
// service needs a Docker container with `caddy` labels for Caddy to route
// to.
func (a *Agent) effectiveServices() []config.ZetaService {
	if p := a.dockerSvcs.Load(); p != nil {
		return *p
	}
	return nil
}

// announceServices sends the effective service list to the controller.
func (a *Agent) announceServices(sendCh chan<- *zetapb.SyncUpdate) {
	svcs := a.effectiveServices()
	decls := make([]*zetapb.ServiceDecl, 0, len(svcs))
	for _, s := range svcs {
		decls = append(decls, &zetapb.ServiceDecl{
			Name:             s.Name,
			AllowedHostnames: s.Access,
		})
	}
	select {
	case sendCh <- &zetapb.SyncUpdate{
		Payload: &zetapb.SyncUpdate_Services{
			Services: &zetapb.ServiceAnnounce{Services: decls},
		},
	}:
	default:
	}
}
