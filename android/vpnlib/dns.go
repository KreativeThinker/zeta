package vpnlib

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"time"
)

type dnsHandler struct {
	records  sync.Map // string(hostname) → string(dotted-quad IP)
	upstream string   // "ip:port"
	dev      *androidTUN
}

// handle processes an intercepted DNS query packet and writes the reply back into the TUN.
// pkt is a complete IPv4 packet (including IPv4+UDP headers).
func (h *dnsHandler) handle(pkt []byte) {
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 {
		return
	}

	// IPv4 addresses and ports for crafting the reply.
	srcIP := make([]byte, 4)
	dstIP := make([]byte, 4)
	copy(srcIP, pkt[12:16])
	copy(dstIP, pkt[16:20])
	srcPort := binary.BigEndian.Uint16(pkt[ihl : ihl+2])
	dstPort := binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])

	udpPayload := pkt[ihl+8:]
	if len(udpPayload) < 12 {
		return
	}

	txID := udpPayload[0:2]
	qdCount := binary.BigEndian.Uint16(udpPayload[4:6])
	if qdCount == 0 {
		return
	}

	// Parse first QNAME from question section (offset 12 in DNS payload).
	name, qnameEnd, ok := parseDNSName(udpPayload, 12)
	if !ok {
		return
	}
	if len(udpPayload) < qnameEnd+4 {
		return
	}
	qtype := binary.BigEndian.Uint16(udpPayload[qnameEnd : qnameEnd+2])

	var replyDNS []byte

	if qtype == 1 && strings.HasSuffix(name, ".mesh") {
		// A query for a .mesh hostname — serve from in-memory table.
		if val, ok := h.records.Load(name); ok {
			ip := net.ParseIP(val.(string)).To4()
			if ip != nil {
				replyDNS = buildAReply(txID, udpPayload[12:qnameEnd+4], name, ip)
			} else {
				replyDNS = buildNXDomain(txID, udpPayload[12:qnameEnd+4])
			}
		} else {
			replyDNS = buildNXDomain(txID, udpPayload[12:qnameEnd+4])
		}
	} else {
		// Non-mesh or non-A query — forward to upstream DNS.
		replyDNS = forwardDNS(udpPayload, h.upstream)
		if replyDNS == nil {
			return
		}
	}

	// Wrap DNS response in UDP + IPv4 headers (swap src/dst).
	reply := buildIPv4UDPPacket(dstIP, srcIP, dstPort, srcPort, replyDNS)

	// Write directly into TUN as an inbound packet.  tun.Device.Write delivers
	// packets to the Android IP stack the same way decrypted WireGuard traffic does.
	h.dev.Write([][]byte{reply}, 0) //nolint:errcheck
}

// parseDNSName decodes a DNS label-encoded name starting at offset in buf.
// Returns the decoded name (lowercase, with trailing dot stripped), the offset
// immediately after the name+null terminator, and whether parsing succeeded.
func parseDNSName(buf []byte, offset int) (string, int, bool) {
	var sb strings.Builder
	for {
		if offset >= len(buf) {
			return "", 0, false
		}
		length := int(buf[offset])
		if length == 0 {
			offset++
			break
		}
		if length&0xc0 == 0xc0 {
			// Compression pointer — not expected in a fresh query but handle gracefully.
			return "", 0, false
		}
		offset++
		if offset+length > len(buf) {
			return "", 0, false
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(strings.ToLower(string(buf[offset : offset+length])))
		offset += length
	}
	return sb.String(), offset, true
}

// buildAReply constructs a minimal DNS A-record response.
// question is the raw question section bytes (QNAME encoded + QTYPE + QCLASS).
func buildAReply(txID []byte, question []byte, name string, ip net.IP) []byte {
	// Build answer RR: pointer to name (0xc00c), type A, class IN, TTL 60, rdlen 4, ip.
	answer := []byte{
		0xc0, 0x0c, // name pointer to offset 12 (start of question)
		0x00, 0x01, // type A
		0x00, 0x01, // class IN
		0x00, 0x00, 0x00, 0x3c, // TTL 60
		0x00, 0x04, // rdlength 4
	}
	answer = append(answer, ip...)

	buf := make([]byte, 0, 12+len(question)+len(answer))
	buf = append(buf, txID...)
	buf = append(buf, 0x81, 0x80) // flags: QR=1, AA=0, RD=1, RA=1
	buf = append(buf, 0x00, 0x01) // QDCOUNT=1
	buf = append(buf, 0x00, 0x01) // ANCOUNT=1
	buf = append(buf, 0x00, 0x00) // NSCOUNT=0
	buf = append(buf, 0x00, 0x00) // ARCOUNT=0
	buf = append(buf, question...)
	buf = append(buf, answer...)
	return buf
}

// buildNXDomain constructs a DNS NXDOMAIN response.
func buildNXDomain(txID []byte, question []byte) []byte {
	buf := make([]byte, 0, 12+len(question))
	buf = append(buf, txID...)
	buf = append(buf, 0x81, 0x83) // flags: QR=1, RD=1, RA=1, RCODE=3 (NXDOMAIN)
	buf = append(buf, 0x00, 0x01) // QDCOUNT=1
	buf = append(buf, 0x00, 0x00) // ANCOUNT=0
	buf = append(buf, 0x00, 0x00) // NSCOUNT=0
	buf = append(buf, 0x00, 0x00) // ARCOUNT=0
	buf = append(buf, question...)
	return buf
}

// forwardDNS sends the raw DNS payload to upstream and returns the response.
func forwardDNS(payload []byte, upstream string) []byte {
	conn, err := net.DialTimeout("udp", upstream, 3*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	if _, err := conn.Write(payload); err != nil {
		return nil
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// buildIPv4UDPPacket wraps a DNS payload in UDP and IPv4 headers.
func buildIPv4UDPPacket(srcIP, dstIP []byte, srcPort, dstPort uint16, payload []byte) []byte {
	udpLen := uint16(8 + len(payload))
	totalLen := uint16(20) + udpLen

	pkt := make([]byte, totalLen)

	// IPv4 header
	pkt[0] = 0x45              // version=4, IHL=5
	pkt[1] = 0x00              // DSCP/ECN
	binary.BigEndian.PutUint16(pkt[2:4], totalLen)
	pkt[4] = 0x00              // identification high
	pkt[5] = 0x00              // identification low
	pkt[6] = 0x40              // flags: DF
	pkt[7] = 0x00              // fragment offset
	pkt[8] = 64                // TTL
	pkt[9] = 17                // protocol UDP
	// checksum at [10:12] — filled below
	copy(pkt[12:16], srcIP)
	copy(pkt[16:20], dstIP)
	binary.BigEndian.PutUint16(pkt[10:12], ipv4Checksum(pkt[:20]))

	// UDP header
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	binary.BigEndian.PutUint16(pkt[24:26], udpLen)
	// UDP checksum omitted (0 = valid for IPv4 UDP)
	pkt[26] = 0x00
	pkt[27] = 0x00

	copy(pkt[28:], payload)
	return pkt
}

// ipv4Checksum computes the one's-complement checksum of the IPv4 header.
func ipv4Checksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i < len(header); i += 2 {
		sum += uint32(header[i])<<8 | uint32(header[i+1])
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
