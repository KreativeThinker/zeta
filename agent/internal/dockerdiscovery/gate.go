package dockerdiscovery

import (
	"strconv"
	"strings"
)

// gate makes each Caddy site of a private container plain http (no ACME for .mesh) and aborts peers outside meshCIDR.
func gate(labels map[string]string, meshCIDR string) {
	if !isPrivate(labels) {
		return
	}
	var keys []string
	for key := range labels {
		if key == labelCaddy || isIndexedCaddyLabel(key) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		addrs := strings.Fields(labels[key])
		for i, a := range addrs {
			if !strings.Contains(a, "://") {
				addrs[i] = "http://" + a
			}
		}
		labels[key] = strings.Join(addrs, " ")
		labels[key+".@zeta_ext.not.remote_ip"] = meshCIDR
		labels[key+".abort"] = "@zeta_ext"
	}
}

func isPrivate(labels map[string]string) bool {
	private, _ := strconv.ParseBool(labels[labelPrivate])
	return private
}
