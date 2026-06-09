package dns

// Resolver is an in-process DNS server for the mesh zone.
// It answers from a local cache of the NetworkMap and forwards
// everything else upstream.
type Resolver struct{}

func New(listenAddr, upstream string) *Resolver {
	return &Resolver{}
}

func (r *Resolver) Start() error {
	// TODO: listen on listenAddr, serve *.mesh from cache, forward rest
	return nil
}
