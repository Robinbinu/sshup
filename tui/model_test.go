package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sshup/sshup/checker"
	"github.com/sshup/sshup/config"
	"github.com/sshup/sshup/tui"
)

func makeHosts() []config.Host {
	return []config.Host{
		{Alias: "web-1", HostName: "192.168.1.1", User: "root", Port: 22},
		{Alias: "db-1", HostName: "10.0.0.1", User: "root", Port: 22},
	}
}

func TestNew_allHostsPending(t *testing.T) {
	m := tui.New(makeHosts(), 10*time.Second, 30*time.Second)
	view := m.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
	// Both hosts should appear
	for _, alias := range []string{"web-1", "db-1"} {
		if !strings.Contains(view, alias) {
			t.Errorf("View() missing host %q", alias)
		}
	}
}

func TestUpdate_resultMsgUpdatesRow(t *testing.T) {
	m := tui.New(makeHosts(), 10*time.Second, 30*time.Second)

	// Simulate a completed check result
	result := checker.Result{
		Alias:    "web-1",
		Status:   checker.StatusUp,
		Uptime:   "3 days, 2:49",
		Load:     1.25,
		MemUsed:  578,
		MemTotal: 8008,
		DiskPct:  62,
	}

	updated, _ := m.Update(tui.ResultMsg(result))
	view := updated.(tui.Model).View()

	if !strings.Contains(view, "web-1") {
		t.Error("updated view missing web-1")
	}
	if !strings.Contains(view, "UP") {
		t.Error("updated view missing UP status")
	}
}

func TestUpdate_quitKey(t *testing.T) {
	m := tui.New(makeHosts(), 10*time.Second, 30*time.Second)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Error("q key should return a Quit cmd")
	}
}
