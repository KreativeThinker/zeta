package coordinator

import (
	"sync"

	"github.com/kreativethinker/zeta/controller/internal/ca"
	"github.com/kreativethinker/zeta/controller/internal/config"
	"github.com/kreativethinker/zeta/controller/internal/db"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

// Coordinator is the central state manager. It owns the live peer registry and
// fans out NetworkMap updates to all connected Sync streams.
type Coordinator struct {
	db  *db.DB
	ca  *ca.CA
	cfg *config.Config

	mu           sync.RWMutex
	streams      map[string]chan *zetapb.SyncResponse // nodeID → buffered send channel
	online       map[string]bool
	relayStreams map[string]chan *zetapb.RelayFrame // nodeID → buffered relay send channel
}

func New(database *db.DB, authority *ca.CA, cfg *config.Config) *Coordinator {
	return &Coordinator{
		db:           database,
		ca:           authority,
		cfg:          cfg,
		streams:      make(map[string]chan *zetapb.SyncResponse),
		online:       make(map[string]bool),
		relayStreams: make(map[string]chan *zetapb.RelayFrame),
	}
}

// IsOnline reports whether a node currently has an active Sync stream.
func (c *Coordinator) IsOnline(nodeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.online[nodeID]
}
