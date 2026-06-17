package vpnlib

import (
	"os"

	"golang.zx2c4.com/wireguard/tun"
)

// androidTUN wraps an Android VPN service fd as a tun.Device.
// It never calls TUNGETIFF or any other ioctl — those require CAP_NET_ADMIN
// which Android apps do not have. Android VPN fds from VpnService.Builder.establish()
// support only plain read()/write() for packet I/O.
type androidTUN struct {
	file   *os.File
	mtu    int
	events chan tun.Event
}

func newAndroidTUN(file *os.File, mtu int) *androidTUN {
	t := &androidTUN{
		file:   file,
		mtu:    mtu,
		events: make(chan tun.Event, 1),
	}
	t.events <- tun.EventUp
	return t
}

// Read reads one packet per call into bufs[0][offset:] and sets sizes[0].
// wireguard-go is fine receiving n=1 even when len(bufs) > 1.
func (t *androidTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if len(bufs) == 0 {
		return 0, nil
	}
	n, err := t.file.Read(bufs[0][offset:])
	sizes[0] = n
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// Write delivers packets to the Android IP stack by writing to the TUN fd.
func (t *androidTUN) Write(bufs [][]byte, offset int) (int, error) {
	for i, buf := range bufs {
		if _, err := t.file.Write(buf[offset:]); err != nil {
			return i, err
		}
	}
	return len(bufs), nil
}

func (t *androidTUN) File() *os.File           { return t.file }
func (t *androidTUN) MTU() (int, error)        { return t.mtu, nil }
func (t *androidTUN) Name() (string, error)    { return "vpn", nil }
func (t *androidTUN) Events() <-chan tun.Event { return t.events }
func (t *androidTUN) BatchSize() int           { return 1 }

func (t *androidTUN) Close() error {
	close(t.events)
	return t.file.Close()
}
