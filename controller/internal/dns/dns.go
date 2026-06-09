package dns

// Server is the authoritative DNS server for the mesh zone (e.g. *.mesh).
type Server struct{}

func New(zone string) *Server {
	return &Server{}
}

func (s *Server) Start(addr string) error {
	// TODO: serve DNS on addr (UDP + TCP)
	return nil
}
