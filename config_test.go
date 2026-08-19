package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configData := `
reset_metrics: false
groups:
  - name: production
    endpoint: tcp://prod:1883
    user: exporter
    pass: secret
  - name: staging
    endpoint: ssl://staging:8883
    username: exporter-staging
    password: staging-secret
`
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if config.BindAddress != defaultBindAddress {
		t.Fatalf("BindAddress = %q, want %q", config.BindAddress, defaultBindAddress)
	}
	if config.shouldResetMetrics() {
		t.Fatal("shouldResetMetrics() = true, want false")
	}
	if len(config.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2", len(config.Groups))
	}
	if config.Groups[0].Username != "exporter" || config.Groups[0].Password != "secret" {
		t.Fatalf("legacy username aliases were not normalized: %#v", config.Groups[0])
	}
}

func TestLoadConfigUsesEmbeddedDefault(t *testing.T) {
	config, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}

	if config.BindAddress != "0.0.0.0:9234" {
		t.Fatalf("BindAddress = %q, want 0.0.0.0:9234", config.BindAddress)
	}
	if len(config.Groups) != 1 || config.Groups[0].Name != "default" {
		t.Fatalf("embedded groups = %#v, want the default group", config.Groups)
	}
}

func TestLoadConfigOverrideKeepsUnsetDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
bind_address: "127.0.0.1:19234"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.BindAddress != "127.0.0.1:19234" {
		t.Fatalf("BindAddress = %q, want 127.0.0.1:19234", config.BindAddress)
	}
	if len(config.Groups) != 1 || config.Groups[0].Endpoint != "tcp://127.0.0.1:1883" {
		t.Fatalf("default groups were not retained: %#v", config.Groups)
	}
}

func TestLoadConfigRejectsDuplicateGroups(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
groups:
  - name: duplicate
    endpoint: tcp://one:1883
  - name: duplicate
    endpoint: tcp://two:1883
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadConfig(configPath)
	if err == nil || !strings.Contains(err.Error(), `duplicate MQTT group name "duplicate"`) {
		t.Fatalf("loadConfig() error = %v, want duplicate group error", err)
	}
}
