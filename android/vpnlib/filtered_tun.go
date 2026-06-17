package vpnlib

import (
	"encoding/binary"
)

// filteredTUN wraps androidTUN and intercepts DNS packets before wireguard-go sees them.
type filteredTUN struct {
	*androidTUN
	handler *dnsHandler
}

// Read intercepts UDP packets destined for 100.64.0.1:53 and dispatches them
// asynchronously via the DNS handler. All other packets are returned to wireguard-go.
func (f *filteredTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	n, err := f.androidTUN.Read(bufs, sizes, offset)
	if n == 0 {
		return n, err
	}
	out := 0
	for i := 0; i < n; i++ {
		pkt := bufs[i][offset : offset+sizes[i]]
		if isDNSQuery(pkt) {
			// Copy before the buffer is reused by the next Read call.
			cp := make([]byte, len(pkt))
			copy(cp, pkt)
			go f.handler.handle(cp)
			continue
		}
		if out != i {
			copy(bufs[out][offset:], pkt)
			sizes[out] = sizes[i]
		}
		out++
	}
	return out, err
}

// isDNSQuery returns true if pkt is an IPv4 UDP packet destined for 100.64.0.1:53.
func isDNSQuery(pkt []byte) bool {
	// Minimum: 20-byte IPv4 header + 8-byte UDP header + 1 DNS byte
	if len(pkt) < 29 {
		return false
	}
	if pkt[0]>>4 != 4 {
		return false
	}
	// Protocol field: 17 = UDP
	if pkt[9] != 17 {
		return false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 {
		return false
	}
	// Destination IP must be 100.64.0.1
	if pkt[16] != 100 || pkt[17] != 64 || pkt[18] != 0 || pkt[19] != 1 {
		return false
	}
	// Destination port must be 53
	dstPort := binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])
	return dstPort == 53
}
