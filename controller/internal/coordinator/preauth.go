package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/kreativethinker/zeta/controller/internal/db"
)

// GeneratePreauthKey creates and persists a new preauth key.
func (c *Coordinator) GeneratePreauthKey(label string, ttl time.Duration, reusable bool) (*db.PreauthKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	k := db.PreauthKey{
		Key:      hex.EncodeToString(raw),
		Label:    label,
		Reusable: reusable,
		Expiry:   time.Now().Add(ttl),
	}
	if err := c.db.CreatePreauthKey(k); err != nil {
		return nil, err
	}
	return &k, nil
}
