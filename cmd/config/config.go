package config

import (
	"errors"
	"io"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/lesomnus/z"
)

var DefaultConfigPaths = []string{
	"wfm.yaml",
	"wfm.yml",
}

type Config struct {
	path string

	Otel OtelConfig
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
	return nil
}
