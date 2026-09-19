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

func parseContainer(c containerSummary) []config.ZetaService {
	if public, _ := strconv.ParseBool(c.Labels[labelPublic]); public {
		return nil
	}

	access := []string{"*"}
	if raw := c.Labels[labelAccess]; raw != "" {
		access = strings.Split(raw, ",")
		for i := range access {
			access[i] = strings.TrimSpace(access[i])
		}
	}

	var svcs []config.ZetaService
	for key, hostname := range c.Labels {
		if key != labelCaddy && !isIndexedCaddyLabel(key) {
			continue
		}
		if idx := strings.Index(hostname, "://"); idx != -1 {
			hostname = hostname[idx+len("://"):]
		}
		name := strings.SplitN(hostname, ".", 2)[0]
		svcs = append(svcs, config.ZetaService{Name: name, Access: access})
	}
	return svcs
}

func isIndexedCaddyLabel(key string) bool {
	return strings.HasPrefix(key, labelCaddy+"_") && !strings.Contains(key, ".")
}
