package db

import (
	"fmt"
	"math/big"
	"net"
)

// AllocateIP finds the lowest available address in cidr above controllerIP.
func (d *DB) AllocateIP(cidr, controllerIP string) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid cidr %q: %w", cidr, err)
	}

	rows, err := d.conn.Query(`SELECT mesh_ip FROM devices`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	used := map[string]bool{controllerIP: true}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return "", err
		}
		used[ip] = true
	}

	ip, err := firstFreeIP(network, used)
	if err != nil {
		return "", fmt.Errorf("%w in %s", err, cidr)
	}
	return ip.String(), nil
}

// firstFreeIP walks network starting right after the network address,
// skipping the reserved controller slot, and returns the first address not
// in used. Pure function — no I/O, same input always yields the same output.
func firstFreeIP(network *net.IPNet, used map[string]bool) (net.IP, error) {
	ip := cloneIP(network.IP)
	incIP(ip) // skip network address
	incIP(ip) // skip controller IP slot (100.64.0.1 reserved even if not in used map)

	for network.Contains(ip) {
		if !used[ip.String()] {
			return ip, nil
		}
		ip = cloneIP(ip)
		incIP(ip)
	}
	return nil, fmt.Errorf("address pool exhausted")
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incIP(ip net.IP) {
	// treat as big-endian integer and add 1
	n := new(big.Int).SetBytes(ip)
	n.Add(n, big.NewInt(1))
	b := n.Bytes()
	// pad to original length
	padded := make([]byte, len(ip))
	copy(padded[len(padded)-len(b):], b)
	copy(ip, padded)
}
