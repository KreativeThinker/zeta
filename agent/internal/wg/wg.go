package wg

import (
	"encoding/base64"
	"fmt"
	"net"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

var keepalive = 25 * time.Second

type Manager struct {
	iface  string
	client *wgctrl.Client
}

type PeerConfig struct {
	PublicKey  string
	AllowedIPs []string
	Endpoint   string
}

func New(iface string) (*Manager, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("wgctrl: %w", err)
	}
	return &Manager{iface: iface, client: c}, nil
}

func (m *Manager) EnsureInterface() error {
	_, err := netlink.LinkByName(m.iface)
	if err == nil {
		return nil
	}
	wgLink := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{Name: m.iface},
		LinkType:  "wireguard",
	}
	if err := netlink.LinkAdd(wgLink); err != nil {
		return fmt.Errorf("adding WireGuard link %s: %w", m.iface, err)
	}
	link, err := netlink.LinkByName(m.iface)
	if err != nil {
		return fmt.Errorf("fetching link %s: %w", m.iface, err)
	}
	return netlink.LinkSetUp(link)
}

func (m *Manager) Configure(privateKeyB64 string, listenPort int) error {
	key, err := wgtypes.ParseKey(privateKeyB64)
	if err != nil {
		return fmt.Errorf("parsing private key: %w", err)
	}
	return m.client.ConfigureDevice(m.iface, wgtypes.Config{
		PrivateKey: &key,
		ListenPort: &listenPort,
	})
}

func (m *Manager) ApplyPeers(peers []PeerConfig) error {
	var wgPeers []wgtypes.PeerConfig
	for _, p := range peers {
		keyBytes, err := base64.StdEncoding.DecodeString(p.PublicKey)
		if err != nil {
			return fmt.Errorf("decoding pubkey for peer: %w", err)
		}
		var key wgtypes.Key
		copy(key[:], keyBytes)

		var allowedIPs []net.IPNet
		for _, cidr := range p.AllowedIPs {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("parsing allowed IP %q: %w", cidr, err)
			}
			allowedIPs = append(allowedIPs, *ipNet)
		}

		pc := wgtypes.PeerConfig{
			PublicKey:                   key,
			ReplaceAllowedIPs:           true,
			AllowedIPs:                  allowedIPs,
			PersistentKeepaliveInterval: &keepalive,
		}

		if p.Endpoint != "" {
			addr, err := net.ResolveUDPAddr("udp", p.Endpoint)
			if err != nil {
				return fmt.Errorf("resolving endpoint %q: %w", p.Endpoint, err)
			}
			pc.Endpoint = addr
		}

		wgPeers = append(wgPeers, pc)
	}

	return m.client.ConfigureDevice(m.iface, wgtypes.Config{
		ReplacePeers: true,
		Peers:        wgPeers,
	})
}

func (m *Manager) AssignAddress(meshIP, cidr string) error {
	link, err := netlink.LinkByName(m.iface)
	if err != nil {
		return fmt.Errorf("getting link %s: %w", m.iface, err)
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}
	ip := net.ParseIP(meshIP)
	if ip == nil {
		return fmt.Errorf("invalid mesh IP %s", meshIP)
	}

	addr := &netlink.Addr{
		IPNet: &net.IPNet{IP: ip, Mask: network.Mask},
	}

	existing, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}
	for _, a := range existing {
		if a.IP.Equal(ip) {
			return nil
		}
	}

	return netlink.AddrAdd(link, addr)
}

func (m *Manager) Close() error {
	return m.client.Close()
}
