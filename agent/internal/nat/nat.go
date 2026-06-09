package nat

import (
	"fmt"
	"net"

	"github.com/pion/stun/v3"
)

const DefaultSTUN = "stun.l.google.com:19302"

// DiscoverEndpoint sends a STUN binding request and returns the observed
// external "ip:port" string. Returns "" on failure (non-fatal).
func DiscoverEndpoint(stunServer string) (string, error) {
	c, err := stun.Dial("udp", stunServer)
	if err != nil {
		return "", fmt.Errorf("dialing STUN server %s: %w", stunServer, err)
	}
	defer c.Close()

	var xorAddr stun.XORMappedAddress
	var mappedAddr stun.MappedAddress

	msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
	if err != nil {
		return "", fmt.Errorf("building STUN request: %w", err)
	}

	if err := c.Do(msg, func(res stun.Event) {
		if res.Error != nil {
			err = res.Error
			return
		}
		if getErr := xorAddr.GetFrom(res.Message); getErr == nil {
			return
		}
		_ = mappedAddr.GetFrom(res.Message)
	}); err != nil {
		return "", fmt.Errorf("STUN transaction: %w", err)
	}

	var ip net.IP
	var port int

	if xorAddr.IP != nil {
		ip = xorAddr.IP
		port = xorAddr.Port
	} else if mappedAddr.IP != nil {
		ip = mappedAddr.IP
		port = mappedAddr.Port
	} else {
		return "", fmt.Errorf("no address in STUN response")
	}

	return fmt.Sprintf("%s:%d", ip.String(), port), nil
}
