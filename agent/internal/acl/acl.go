package acl

// Engine enforces peer-local ACL rules against inbound connections.
// Rules reference user IDs resolved from the coordinator's identity map.
type Engine struct{}

func New() *Engine {
	return &Engine{}
}
