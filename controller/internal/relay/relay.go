package relay

// Server forwards encrypted WireGuard UDP packets between peers by public key.
// It never decrypts traffic.
type Server struct{}

func New() *Server {
	return &Server{}
}

func (s *Server) Start(addr string) error {
	// TODO: key-addressed UDP relay (DERP-style)
	return nil
}
