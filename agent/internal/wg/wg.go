package wg

// Manager configures the local WireGuard interface via wgctrl.
type Manager struct{}

func New(iface string) (*Manager, error) {
	// TODO: open wgctrl client, create/configure interface
	return &Manager{}, nil
}

func (m *Manager) Close() error { return nil }
