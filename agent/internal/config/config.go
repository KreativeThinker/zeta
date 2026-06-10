package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Coordinator CoordinatorConfig `yaml:"coordinator"`
	WireGuard   WGConfig          `yaml:"wireguard"`
	DNS         DNSConfig         `yaml:"dns"`
	State       StateConfig       `yaml:"state"`
	HTTP        HTTPConfig        `yaml:"http"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"` // agent UI listen address
}

type CoordinatorConfig struct {
	Addr string `yaml:"addr"`
}

type WGConfig struct {
	Interface  string `yaml:"interface"`
	ListenPort int    `yaml:"listen_port"`
}

type DNSConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	Upstream   string `yaml:"upstream"`
}

type StateConfig struct {
	Path string `yaml:"path"`
}

func defaults() *Config {
	return &Config{
		Coordinator: CoordinatorConfig{Addr: "localhost:50051"},
		WireGuard:   WGConfig{Interface: "zeta0", ListenPort: 51820},
		DNS:         DNSConfig{ListenAddr: "127.0.0.1:53", Upstream: "1.1.1.1:53"},
		State:       StateConfig{Path: "/var/lib/zeta/state.json"},
		HTTP:        HTTPConfig{Addr: "127.0.0.1:6080"},
	}
}

func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, err
			}
		}
	}

	// Environment overrides.
	if v := os.Getenv("ZETA_COORDINATOR"); v != "" {
		cfg.Coordinator.Addr = v
	}
	if v := os.Getenv("ZETA_WG_INTERFACE"); v != "" {
		cfg.WireGuard.Interface = v
	}
	if v := os.Getenv("ZETA_STATE_PATH"); v != "" {
		cfg.State.Path = v
	}
	if v := os.Getenv("ZETA_DNS_LISTEN"); v != "" {
		cfg.DNS.ListenAddr = v
	}
	if v := os.Getenv("ZETA_DNS_UPSTREAM"); v != "" {
		cfg.DNS.Upstream = v
	}
	if v := os.Getenv("ZETA_HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}

	return cfg, nil
}
