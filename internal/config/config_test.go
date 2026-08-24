package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestValidate_Errors(t *testing.T) {
	c := Default()
	c.OTLP.HTTP = c.OTLP.GRPC
	if err := c.Validate(); err == nil {
		t.Error("expected error when grpc==http")
	}

	c = Default()
	c.OTLP.GRPC = "not-a-hostport"
	if err := c.Validate(); err == nil {
		t.Error("expected error for malformed address")
	}

	c = Default()
	c.DB.Path = ""
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty db path")
	}
}

func TestLoad_OverlaysDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	// Only override the HTTP port; everything else should keep defaults.
	if err := os.WriteFile(path, []byte("otlp:\n  http: \":9999\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OTLP.HTTP != ":9999" {
		t.Errorf("HTTP = %q, want :9999", c.OTLP.HTTP)
	}
	if c.OTLP.GRPC != ":4317" {
		t.Errorf("GRPC default lost: %q", c.OTLP.GRPC)
	}
	if !c.Ingest.CaptureContent {
		t.Error("capture_content default should remain true")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("loaded config should validate: %v", err)
	}
}
