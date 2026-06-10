package nat

import (
	"fmt"
	"net"

	"github.com/pion/stun/v3"
)

const DefaultSTUN = "stun.l.google.com:19302"

// DiscoverEndpoint sends a STUN binding request to learn the external IP,
// then returns "ip:wgPort".
func DiscoverEndpoint(stunServer string, wgPort int) (string, error) {
	return doSTUN(stunServer, "udp", wgPort)
}

func doSTUN(stunServer, network string, wgPort int) (string, error) {
	c, err := stun.Dial(network, stunServer)
	if err != nil {
		return "", fmt.Errorf("dialing STUN server %s: %w", stunServer, err)
	}
	defer c.Close()

	var xorAddr stun.XORMappedAddress
	var mappedAddr stun.MappedAddress
	var eventErr error

	msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
	if err != nil {
		return "", fmt.Errorf("building STUN request: %w", err)
	}

	if err := c.Do(msg, func(res stun.Event) {
		if res.Error != nil {
			eventErr = res.Error
			return
		}
		if getErr := xorAddr.GetFrom(res.Message); getErr == nil {
			return
		}
		_ = mappedAddr.GetFrom(res.Message)
	}); err != nil {
		return "", fmt.Errorf("STUN transaction: %w", err)
	}
	if eventErr != nil {
		return "", fmt.Errorf("STUN event: %w", eventErr)
	}

	var ip net.IP
	if xorAddr.IP != nil {
		ip = xorAddr.IP
	} else if mappedAddr.IP != nil {
		ip = mappedAddr.IP
	} else {
		return "", fmt.Errorf("no address in STUN response")
	}

	return net.JoinHostPort(ip.String(), fmt.Sprintf("%d", wgPort)), nil
}
