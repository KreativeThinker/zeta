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

// effectiveServices merges zetafile services with Docker-label-discovered
// services. Docker-declared services win on name collision since they
// reflect live container state.
func (a *Agent) effectiveServices() []config.ZetaService {
	var zfSvcs []config.ZetaService
	if zf := a.zf.Load(); zf != nil {
		zfSvcs = zf.Services
	}
	var dockerSvcs []config.ZetaService
	if p := a.dockerSvcs.Load(); p != nil {
		dockerSvcs = *p
	}
	if len(dockerSvcs) == 0 {
		return zfSvcs
	}
	merged := make(map[string]config.ZetaService, len(zfSvcs)+len(dockerSvcs))
	for _, s := range zfSvcs {
		merged[s.Name] = s
	}
	for _, s := range dockerSvcs {
		merged[s.Name] = s
	}
	out := make([]config.ZetaService, 0, len(merged))
	for _, s := range merged {
		out = append(out, s)
	}
	return out
}

// announceServices sends the effective service list to the controller.
func (a *Agent) announceServices(sendCh chan<- *zetapb.SyncUpdate) {
	svcs := a.effectiveServices()
	decls := make([]*zetapb.ServiceDecl, 0, len(svcs))
	for _, s := range svcs {
		decls = append(decls, &zetapb.ServiceDecl{
			Name:             s.Name,
			TargetAddr:       s.Target,
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
