package proxy

import "time"

const maxEvents = 500

// AccessEvent records a single inbound connection attempt.
type AccessEvent struct {
	Time     time.Time `json:"time"`
	Service  string    `json:"service"`
	SourceIP string    `json:"source_ip"`
	Hostname string    `json:"hostname"`
	Allowed  bool      `json:"allowed"`
}

// RecentEvents returns up to n most recent access events (newest first).
func (m *Manager) RecentEvents(n int) []AccessEvent {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	if n <= 0 || n > len(m.events) {
		n = len(m.events)
	}
	out := make([]AccessEvent, n)
	for i := 0; i < n; i++ {
		out[i] = m.events[len(m.events)-1-i]
	}
	return out
}

func (m *Manager) record(ev AccessEvent) {
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if len(m.events) >= maxEvents {
		m.events = m.events[1:]
	}
	m.events = append(m.events, ev)
}
