package config_test

import (
	"os"
	"testing"

	"github.com/sshup/sshup/config"
)

const sampleConfig = `
Host web-1
  HostName 192.168.1.1
  User deploy
  Port 2222
  IdentityFile ~/.ssh/id_ed25519

Host db-1
  HostName 10.0.0.1
  User root

Host *
  ServerAliveInterval 60
`

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()

	file, err := os.CreateTemp("", "sshup-config-*")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(file.Name())
	})

	if _, err := file.WriteString(contents); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp config: %v", err)
	}

	return file.Name()
}

func TestParse_returnsAllNonWildcardHosts(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
}

func TestParse_hostFields(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	got := hosts[0]
	if got.Alias != "web-1" {
		t.Errorf("Alias = %q, want %q", got.Alias, "web-1")
	}
	if got.HostName != "192.168.1.1" {
		t.Errorf("HostName = %q, want %q", got.HostName, "192.168.1.1")
	}
	if got.User != "deploy" {
		t.Errorf("User = %q, want %q", got.User, "deploy")
	}
	if got.Port != 2222 {
		t.Errorf("Port = %d, want %d", got.Port, 2222)
	}
}

func TestParse_defaultsWhenOmitted(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	got := hosts[1]
	if got.Alias != "db-1" {
		t.Fatalf("Alias = %q, want %q", got.Alias, "db-1")
	}
	if got.Port != 22 {
		t.Errorf("Port = %d, want %d", got.Port, 22)
	}
}

func TestParse_skipsWildcard(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	for _, host := range hosts {
		if host.Alias == "*" {
			t.Fatalf("expected wildcard host to be skipped")
		}
	}
}

func TestParse_missingFile(t *testing.T) {
	_, err := config.Parse("/nonexistent/path/config")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
}
