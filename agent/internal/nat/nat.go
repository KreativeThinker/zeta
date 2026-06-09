package nat

// Traversal manages ICE candidate gathering and hole-punch attempts per peer.
type Traversal struct{}

func New(stunServer string) *Traversal {
	return &Traversal{}
}
