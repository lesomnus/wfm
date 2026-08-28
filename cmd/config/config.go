package config

import (
	"errors"
	"io"
	"os"
	"strings"

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
	Ubus      UbusConfig      `yaml:"ubus"`
}

// UbusConfig configures the OpenWrt ubus-over-HTTP backend (--backend ubus). It
// is only consulted when that backend is selected.
type UbusConfig struct {
	// Endpoint is the ubus JSON-RPC URL, e.g. "http://192.168.1.1/ubus".
	Endpoint string `yaml:"endpoint,omitempty"`
	Username string `yaml:"username,omitempty"`
	// PasswordFile is the path to a file whose contents are the password. It is
	// preferred over Password so the secret is not printed by `wfm config`.
	PasswordFile string `yaml:"password_file,omitempty"`
	Password     string `yaml:"password,omitempty"`
	// Insecure skips TLS verification, for a node serving a self-signed cert.
	Insecure bool `yaml:"insecure,omitempty"`
	// Radio is the wifi-device new station profiles attach to ("" = first
	// found); Network is the network interface a station binds to for DHCP
	// ("" defaults to "wwan").
	Radio   string `yaml:"radio,omitempty"`
	Network string `yaml:"network,omitempty"`
}

// ResolvePassword returns the password, reading PasswordFile when set (it wins
// over an inline Password).
func (c UbusConfig) ResolvePassword() (string, error) {
	if c.PasswordFile == "" {
		return c.Password, nil
	}
	b, err := os.ReadFile(c.PasswordFile)
	if err != nil {
		return "", z.Err(err, "read ubus password_file")
	}
	return strings.TrimRight(string(b), "\r\n"), nil
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
