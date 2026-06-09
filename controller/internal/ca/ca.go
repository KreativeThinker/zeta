package ca

// CA is a minimal in-process certificate authority.
// It issues short-lived device certificates binding user_id + device_id + WireGuard public key.
type CA struct{}

func New(certPEM, keyPEM []byte) (*CA, error) {
	// TODO: load or generate root CA keypair
	return &CA{}, nil
}
