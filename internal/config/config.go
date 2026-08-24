// Package config loads and validates the AgentXRay YAML configuration.
package config

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

// Config mirrors config/agentxray.example.yaml. All fields have defaults.
type Config struct {
	OTLP struct {
		GRPC string `yaml:"grpc"`
		HTTP string `yaml:"http"`
	} `yaml:"otlp"`
	DB struct {
		Path string `yaml:"path"`
	} `yaml:"db"`
	Ingest struct {
		CaptureContent bool `yaml:"capture_content"`
	} `yaml:"ingest"`
	Tokenizer struct {
		Encoding string `yaml:"encoding"`
	} `yaml:"tokenizer"`
}

// Default returns a Config populated with the built-in defaults.
func Default() Config {
	var c Config
	c.OTLP.GRPC = ":4317"
	c.OTLP.HTTP = ":4318"
	c.DB.Path = "agentxray.db"
	c.Ingest.CaptureContent = true
	c.Tokenizer.Encoding = "cl100k_base"
	return c
}

// Load reads and parses a config file, filling unset fields with defaults.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read config %s: %w", path, err)
	}
	// Unmarshal over defaults so omitted keys keep their default values.
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}

// Validate checks that the configuration is internally consistent.
func (c Config) Validate() error {
	for label, addr := range map[string]string{"otlp.grpc": c.OTLP.GRPC, "otlp.http": c.OTLP.HTTP} {
		if addr == "" {
			return fmt.Errorf("%s: address must not be empty", label)
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("%s: invalid host:port %q: %w", label, addr, err)
		}
	}
	if c.OTLP.GRPC == c.OTLP.HTTP {
		return fmt.Errorf("otlp.grpc and otlp.http must differ (both %q)", c.OTLP.GRPC)
	}
	if c.DB.Path == "" {
		return fmt.Errorf("db.path must not be empty")
	}
	if c.Tokenizer.Encoding == "" {
		return fmt.Errorf("tokenizer.encoding must not be empty")
	}
	return nil
}
