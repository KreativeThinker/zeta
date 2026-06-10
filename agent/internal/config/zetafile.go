package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func SaveZetafile(path string, zf *Zetafile) error {
	if path == "" {
		path = "zetafile.yml"
	}
	data, err := yaml.Marshal(zf)
	if err != nil {
		return fmt.Errorf("marshalling zetafile: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

type Zetafile struct {
	Services []ZetaService `yaml:"services"`
}

type ZetaService struct {
	Name   string   `yaml:"name"   json:"name"`
	Target string   `yaml:"target" json:"target"`
	Port   int      `yaml:"port"   json:"port"`
	Access []string `yaml:"access" json:"access"`
}

func LoadZetafile(path string) (*Zetafile, error) {
	if path == "" {
		path = "zetafile.yml"
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
