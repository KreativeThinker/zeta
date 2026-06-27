package ca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/kreativethinker/zeta/controller/internal/db"
)

// CA is an in-process certificate authority.
type CA struct {
	cert    *x509.Certificate
	key     crypto.Signer
	certPEM []byte
}

// LoadOrCreate loads the CA from disk paths or DB settings, generating a new
// root CA and persisting it to DB if none exists.
func LoadOrCreate(database *db.DB, certFile, keyFile string) (*CA, error) {
	if certFile != "" && keyFile != "" {
		return loadFromFiles(certFile, keyFile)
	}
	return loadOrCreateFromDB(database)
}

// CertPEM returns the CA certificate in PEM format.
func (c *CA) CertPEM() []byte {
	return c.certPEM
}

// IssueDeviceCert generates an ECDSA keypair for a device, signs a 90-day cert,
// and returns both the cert PEM and private key PEM.
// Phase 3 note: replace with a CSR flow so the private key never leaves the device.
func (c *CA) IssueDeviceCert(nodeID, meshIP, hostname, meshDomain string) (certPEM, keyPEM []byte, err error) {
	return c.issueDeviceCert(nodeID, meshIP, hostname, meshDomain)
}

func (c *CA) issueDeviceCert(nodeID, meshIP, hostname, meshDomain string) (certPEM, keyPEM []byte, err error) {
	deviceKey, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if genErr != nil {
		return nil, nil, fmt.Errorf("generating device key: %w", genErr)
	}

	certPEM, signErr := c.signCert(nodeID, meshIP, hostname, meshDomain, deviceKey.Public())
	if signErr != nil {
		return nil, nil, signErr
	}

	keyDER, marshalErr := x509.MarshalECPrivateKey(deviceKey)
	if marshalErr != nil {
		return nil, nil, fmt.Errorf("marshalling device key: %w", marshalErr)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func (c *CA) signCert(nodeID, meshIP, hostname, meshDomain string, pubKey crypto.PublicKey) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	ip := net.ParseIP(meshIP)
	if ip == nil {
		return nil, fmt.Errorf("invalid mesh IP %q", meshIP)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: nodeID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
		DNSNames: []string{
			fmt.Sprintf("%s.%s", hostname, meshDomain),
			fmt.Sprintf("*.%s.%s", hostname, meshDomain),
		},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, c.cert, pubKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("signing device cert: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), nil
}

// unused import guard
var _ = crypto.Hash(0)

// ── loaders ───────────────────────────────────────────────────────────────────

func loadFromFiles(certFile, keyFile string) (*CA, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA key: %w", err)
	}
	return parsePEM(certPEM, keyPEM)
}

func loadOrCreateFromDB(database *db.DB) (*CA, error) {
	certPEMStr, err := database.GetSetting("ca_cert_pem")
	if err != nil {
		return nil, err
	}
	keyPEMStr, err := database.GetSetting("ca_key_pem")
	if err != nil {
		return nil, err
	}

	if certPEMStr != "" && keyPEMStr != "" {
		return parsePEM([]byte(certPEMStr), []byte(keyPEMStr))
	}

	// Generate new root CA.
	ca, certPEM, keyPEM, err := generate()
	if err != nil {
		return nil, err
	}
	if err := database.SetSetting("ca_cert_pem", string(certPEM)); err != nil {
		return nil, err
	}
	if err := database.SetSetting("ca_key_pem", string(keyPEM)); err != nil {
		return nil, err
	}
	return ca, nil
}

func generate() (*CA, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Zeta Root CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &CA{cert: cert, key: key, certPEM: certPEM}, certPEM, keyPEM, nil
}

func parsePEM(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing CA key: %w", err)
	}

	return &CA{cert: cert, key: key, certPEM: certPEM}, nil
}
