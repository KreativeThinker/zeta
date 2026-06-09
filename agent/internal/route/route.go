package route

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

func AddMeshRoute(cidr, iface string) error {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("getting link %s: %w", iface, err)
	}

	_, dst, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Dst != nil && r.Dst.String() == dst.String() {
			return nil
		}
	}

	return netlink.RouteAdd(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
	})
}

func RemoveMeshRoute(cidr, iface string) error {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("getting link %s: %w", iface, err)
	}

	_, dst, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	return netlink.RouteDel(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
	})
}
