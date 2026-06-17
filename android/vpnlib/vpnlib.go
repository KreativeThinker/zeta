// Package vpnlib provides a WireGuard VPN with in-process split DNS interception.
// It is compiled to an Android AAR via gomobile bind.
package vpnlib

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
)

type instance struct {
	dev     *device.Device
	handler *dnsHandler
}

var (
	mu      sync.Mutex
	handles = map[int]*instance{}
	counter int32
	lastErr string
)

// Start creates a WireGuard device with DNS interception on the given TUN fd.
//
// tunFd is the raw file descriptor from VpnService.Builder.establish().detachFd().
// mtu is the MTU set on the VPN builder (typically 1280).
// wgConf is the WireGuard UAPI configuration string.
// upstreamDNS is "ip:port" for forwarding non-mesh DNS queries (e.g. "1.1.1.1:53").
//
// Returns an opaque handle ≥ 0 on success, or -1 on failure (call Error() for details).
func Start(tunFd, mtu int, wgConf, upstreamDNS string) int {
	tunFile := os.NewFile(uintptr(tunFd), "tun")
	tunDev := newAndroidTUN(tunFile, mtu)

	// dnsHandler writes reply packets back via tunDev.Write, which injects them
	// as inbound packets to the Android IP stack.
	handler := &dnsHandler{upstream: upstreamDNS, dev: tunDev}
	ft := &filteredTUN{androidTUN: tunDev, handler: handler}

	logger := device.NewLogger(device.LogLevelError, "zetavpn: ")
	wgDev := device.NewDevice(ft, conn.NewDefaultBind(), logger)

	if err := wgDev.IpcSet(wgConf); err != nil {
		wgDev.Close()
		lastErr = fmt.Sprintf("IpcSet: %v", err)
		return -1
	}
	wgDev.Up()

	id := int(atomic.AddInt32(&counter, 1))
	mu.Lock()
	handles[id] = &instance{dev: wgDev, handler: handler}
	mu.Unlock()
	return id
}

// Stop tears down the WireGuard device associated with handle.
func Stop(handle int) {
	mu.Lock()
	inst, ok := handles[handle]
	if ok {
		delete(handles, handle)
	}
	mu.Unlock()
	if ok {
		inst.dev.Down()
		inst.dev.Close()
	}
}

// SetConfig reconfigures WireGuard peers without dropping the TUN.
// wgConf must be in UAPI format and should include replace_peers=true to clear stale peers.
func SetConfig(handle int, wgConf string) error {
	mu.Lock()
	inst, ok := handles[handle]
	mu.Unlock()
	if !ok {
		return fmt.Errorf("invalid handle %d", handle)
	}
	return inst.dev.IpcSet(wgConf)
}

// SetDNSRecords replaces the in-memory hostname→IP table used for *.mesh resolution.
// records is a JSON object: {"dozze.shire.mesh": "100.64.x.x", ...}
func SetDNSRecords(handle int, records string) error {
	mu.Lock()
	inst, ok := handles[handle]
	mu.Unlock()
	if !ok {
		return fmt.Errorf("invalid handle %d", handle)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(records), &m); err != nil {
		return fmt.Errorf("SetDNSRecords: %v", err)
	}
	inst.handler.records.Range(func(k, _ any) bool {
		inst.handler.records.Delete(k)
		return true
	})
	for k, v := range m {
		inst.handler.records.Store(k, v)
	}
	return nil
}

// Error returns the last error string from a failed Start call.
func Error() string { return lastErr }

// GeneratePrivateKey returns a Base64-encoded WireGuard private key (32 bytes, Curve25519).
func GeneratePrivateKey() string {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return ""
	}
	// RFC 7748 §5 clamping for X25519.
	key[0] &= 248
	key[31] = (key[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(key[:])
}

// PublicKey derives the Base64-encoded WireGuard public key from a Base64-encoded private key.
func PublicKey(privateKeyB64 string) string {
	priv, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil || len(priv) != 32 {
		return ""
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(pub)
}
