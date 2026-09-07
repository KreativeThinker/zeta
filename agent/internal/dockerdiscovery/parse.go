package dockerdiscovery

import (
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/config"
)

const (
	labelName   = "zeta.service.name"
	labelPort   = "zeta.service.port"
	labelAccess = "zeta.service.access"
)

// parseContainer extracts a ZetaService from a container's zeta.service.*
// labels. Returns ok=false if the container doesn't declare a mesh service.
func parseContainer(c containerSummary) (config.ZetaService, bool) {
	name := c.Labels[labelName]
	portStr := c.Labels[labelPort]
	if name == "" || portStr == "" {
		return config.ZetaService{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		slog.Warn("dockerdiscovery: invalid port label", "container", shortID(c.ID), "port", portStr)
		return config.ZetaService{}, false
	}
	ip := containerIP(c)
	if ip == "" {
		slog.Warn("dockerdiscovery: no network IP for container", "container", shortID(c.ID))
		return config.ZetaService{}, false
	}

	access := []string{"*"}
	if raw := c.Labels[labelAccess]; raw != "" {
		access = strings.Split(raw, ",")
		for i := range access {
			access[i] = strings.TrimSpace(access[i])
		}
	}

	return config.ZetaService{
		Name:   name,
		Target: net.JoinHostPort(ip, strconv.Itoa(port)),
		Access: access,
	}, true
}
