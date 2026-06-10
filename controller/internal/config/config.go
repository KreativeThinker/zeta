package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DB   DBConfig   `yaml:"db"`
	GRPC GRPCConfig `yaml:"grpc"`
	HTTP HTTPConfig `yaml:"http"`
	CA   CAConfig   `yaml:"ca"`
	Mesh MeshConfig `yaml:"mesh"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type GRPCConfig struct {
	Addr string `yaml:"addr"`
}

type HTTPConfig struct {
	Addr          string `yaml:"addr"`
	AdminPassword string `yaml:"admin_password"`
}

type CAConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type MeshConfig struct {
	Domain       string `yaml:"domain"`
	CIDR         string `yaml:"cidr"`
	ControllerIP string `yaml:"controller_ip"`
}

func defaults() *Config {
	return &Config{
		DB:   DBConfig{Path: "zeta.db"},
		GRPC: GRPCConfig{Addr: ":50051"},
		HTTP: HTTPConfig{Addr: ":8080"},
		Mesh: MeshConfig{
			Domain:       "mesh",
			CIDR:         "100.64.0.0/10",
			ControllerIP: "100.64.0.1",
		},
	}
}

// Load reads the YAML config file at path (if it exists), then applies env overrides.
// Missing config file is not an error — defaults are used.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("ZETA_DB_PATH"); v != "" {
		cfg.DB.Path = v
	}
	if v := os.Getenv("ZETA_GRPC_ADDR"); v != "" {
		cfg.GRPC.Addr = v
	}
	if v := os.Getenv("ZETA_HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("ZETA_ADMIN_PASSWORD"); v != "" {
		cfg.HTTP.AdminPassword = v
	}
	if v := os.Getenv("ZETA_CA_CERT_FILE"); v != "" {
		cfg.CA.CertFile = v
	}
	if v := os.Getenv("ZETA_CA_KEY_FILE"); v != "" {
		cfg.CA.KeyFile = v
	}
	if v := os.Getenv("ZETA_MESH_DOMAIN"); v != "" {
		cfg.Mesh.Domain = v
	}
	if v := os.Getenv("ZETA_MESH_CIDR"); v != "" {
		cfg.Mesh.CIDR = v
	}
	if v := os.Getenv("ZETA_MESH_CONTROLLER_IP"); v != "" {
		cfg.Mesh.ControllerIP = v
	}
}
