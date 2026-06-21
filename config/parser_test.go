package config_test

import (
	"os"
	"os/user"
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
	if got.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("IdentityFile = %q, want %q", got.IdentityFile, "~/.ssh/id_ed25519")
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
	if got.IdentityFile != "" {
		t.Errorf("IdentityFile = %q, want empty string", got.IdentityFile)
	}
}

func TestParse_defaultsHostNameToAliasWhenOmitted(t *testing.T) {
	path := writeTempConfig(t, `
Host app-1
  User deploy
`)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	got := hosts[0]
	if got.HostName != "app-1" {
		t.Errorf("HostName = %q, want %q", got.HostName, "app-1")
	}
}

func TestParse_defaultsUserToCurrentOSUserWhenOmitted(t *testing.T) {
	path := writeTempConfig(t, `
Host app-1
  HostName 192.168.1.2
`)

	hosts, err := config.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	got := hosts[0]
	want := currentUsername()
	if got.User != want {
		t.Errorf("User = %q, want %q", got.User, want)
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

func TestParse_skipsWildcardPatternVariants(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		aliases []string
	}{
		{
			name: "star",
			config: `
Host *.internal
  User deploy
`,
			aliases: nil,
		},
		{
			name: "question",
			config: `
Host web-?
  User deploy
`,
			aliases: nil,
		},
		{
			name: "bang",
			config: `
Host !blocked
  User deploy
`,
			aliases: nil,
		},
		{
			name: "mixed concrete and wildcard patterns",
			config: `
Host app-1 *.internal web-? !blocked
  User deploy
`,
			aliases: []string{"app-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.config)

			hosts, err := config.Parse(path)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}

			if len(hosts) != len(tt.aliases) {
				t.Fatalf("expected %d hosts, got %d: %#v", len(tt.aliases), len(hosts), hosts)
			}
			for i, want := range tt.aliases {
				if hosts[i].Alias != want {
					t.Errorf("hosts[%d].Alias = %q, want %q", i, hosts[i].Alias, want)
				}
			}
		})
	}
}

func TestParse_defaultsPortWhenInvalidOrNotPositive(t *testing.T) {
	tests := []struct {
		name string
		port string
	}{
		{name: "invalid", port: "abc"},
		{name: "zero", port: "0"},
		{name: "negative", port: "-22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, `
Host app-1
  Port `+tt.port+`
`)

			hosts, err := config.Parse(path)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}

			got := hosts[0]
			if got.Port != 22 {
				t.Errorf("Port = %d, want %d", got.Port, 22)
			}
		})
	}
}

func TestParse_missingFile(t *testing.T) {
	_, err := config.Parse("/nonexistent/path/config")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
}

func currentUsername() string {
	currentUser, err := user.Current()
	if err != nil || currentUser.Username == "" {
		return "root"
	}

	return currentUser.Username
}
