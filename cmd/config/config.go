package config

import (
	"errors"
	"io"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/lesomnus/wfm/internal/wnet"
	"github.com/lesomnus/z"
)

var DefaultConfigPaths = []string{
	"wfm.yaml",
	"wfm.yml",
}

type Config struct {
	path string

	Otel      OtelConfig      `yaml:"otel"`
	Interface InterfaceConfig `yaml:"interface"`
}

// InterfaceConfig configures how the service treats network interfaces.
type InterfaceConfig struct {
	// Exclude lists interfaces hidden from the service entirely: an excluded
	// interface never appears in any listing and every operation targeting it
	// (or a connection bound to it) behaves as if it does not exist. See
	// wnet.ExcludeRule for the matching rules.
	Exclude []wnet.ExcludeRule `yaml:"exclude,omitempty"`
}

func ReadFromFile(p string) (*Config, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, z.Err(err, "open")
	}

	var c Config
	// An empty or comment-only file is a valid (empty) config.
	if err := yaml.NewDecoder(f).Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, z.Err(err, "decode")
	}

	c.path = p
	return &c, nil
}

func (c *Config) Path() string {
	return c.path
}

func (c *Config) Evaluate() error {
	for i, r := range c.Interface.Exclude {
		if err := r.Validate(); err != nil {
			return z.Err(err, "interface.exclude[%d]", i)
		}
	}
	return nil
}
