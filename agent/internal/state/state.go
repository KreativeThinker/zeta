package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type State struct {
	NodeID       string `json:"node_id"`
	MeshIP       string `json:"mesh_ip"`
	Domain       string `json:"domain"`
	WGPrivateKey string `json:"wg_private_key"`
	WGPublicKey  string `json:"wg_public_key"`
	CertPEM      string `json:"cert_pem"`
	CAPEM        string `json:"ca_pem"`
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	return &s, json.Unmarshal(data, &s)
}

func Save(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func GenerateKeyPair() (priv, pub string, err error) {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	return key.String(), key.PublicKey().String(), nil
}
