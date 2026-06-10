package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Zetafile struct {
	Services []ZetaService `yaml:"services"`
}

type ZetaService struct {
	Name       string   `yaml:"name"`
	Target     string   `yaml:"target"`
	Port       int      `yaml:"port"`
	Access     []string `yaml:"access"` // ["user:graveyard", "user:shire"]
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
