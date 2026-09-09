package dockerdiscovery

import (
	"strconv"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/config"
)

const (
	labelCaddy  = "caddy"
	labelPublic = "zeta.public"
	labelAccess = "zeta.access"
)

// parseContainer extracts a ZetaService from a container's `caddy`/`zeta.*`
// labels. Only private (mesh-only, the default) services are returned —
// public services (zeta.public: "true") are routed by Caddy straight from
// its own labels and never need to be known to zeta's mesh DNS.
func parseContainer(c containerSummary) (config.ZetaService, bool) {
	hostname := c.Labels[labelCaddy]
	if hostname == "" {
		return config.ZetaService{}, false
	}
	if public, _ := strconv.ParseBool(c.Labels[labelPublic]); public {
		return config.ZetaService{}, false
	}
	if idx := strings.Index(hostname, "://"); idx != -1 {
		hostname = hostname[idx+len("://"):]
	}

	name := strings.SplitN(hostname, ".", 2)[0]

	access := []string{"*"}
	if raw := c.Labels[labelAccess]; raw != "" {
		access = strings.Split(raw, ",")
		for i := range access {
			access[i] = strings.TrimSpace(access[i])
		}
	}

	return config.ZetaService{
		Name:   name,
		Access: access,
	}, true
}
