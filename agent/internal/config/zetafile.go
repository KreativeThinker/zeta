package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

var defaultZetafilePaths = []string{"zetafile.yml", "config.zeta", "zetafile.conf"}

func ResolveZetafilePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, name := range defaultZetafilePaths {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return "zetafile.yml"
}

func SaveZetafile(path string, zf *Zetafile) error {
	if path == "" {
		path = ResolveZetafilePath("")
	}
	data, err := yaml.Marshal(zf)
	if err != nil {
		return fmt.Errorf("marshalling zetafile: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

type Zetafile struct {
	Firewall FirewallConfig `yaml:"firewall"`
}

// ZetaService is a mesh service discovered via Docker container labels
// (agent/internal/dockerdiscovery). There is no manually-declared/bare-metal
// path anymore — every service needs a Docker container with `caddy` labels
// for Caddy to route to.
type ZetaService struct {
	Name   string   `yaml:"name"   json:"name"`
	Access []string `yaml:"access" json:"access"`
}

type FirewallConfig struct {
	Backend string         `yaml:"backend"` // "ufw" or "nft"; empty = auto-detect
	Rules   []FirewallRule `yaml:"rules"`
}

type FirewallRule struct {
	Proto string `yaml:"proto"` // tcp, udp, icmp, any
	Port  int    `yaml:"port"`  // 0 = any port
	From  string `yaml:"from"`  // CIDR or "any"; empty = any
}

func LoadZetafile(path string) (*Zetafile, error) {
	if path == "" {
		path = ResolveZetafilePath("")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Zetafile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading zetafile: %w", err)
	}
	var zf Zetafile
	if err := yaml.Unmarshal(data, &zf); err != nil {
		return nil, fmt.Errorf("parsing zetafile: %w", err)
	}
	return &zf, nil
}

func WatchZetafile(ctx context.Context, path string, onChange func(*Zetafile)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	if err := w.Add(path); err != nil {
		w.Close()
		return fmt.Errorf("watching %s: %w", path, err)
	}

	go func() {
		defer w.Close()
		// Debounce: wait a short interval after the last event before reloading,
		// to handle editors that do an atomic rename-into-place.
		var debounce <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) || ev.Has(fsnotify.Rename) {
					debounce = time.After(50 * time.Millisecond)
					// Re-add after rename (some editors replace the file entirely).
					if ev.Has(fsnotify.Rename) {
						_ = w.Add(path)
					}
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				slog.Warn("zetafile watcher error", "err", err)
			case <-debounce:
				debounce = nil
				zf, err := LoadZetafile(path)
				if err != nil {
					slog.Warn("reloading zetafile", "err", err)
					continue
				}
				slog.Info("zetafile reloaded", "path", path, "firewall_rules", len(zf.Firewall.Rules))
				onChange(zf)
			}
		}
	}()

	return nil
}
