package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sshup/sshup/checker"
	"github.com/sshup/sshup/config"
)

// Go 1.22 has builtin max — no local definition needed.

// Message types for the bubbletea event loop.
type (
	startedMsg   struct{ ch <-chan checker.Result }
	ResultMsg    checker.Result // exported so model_test.go can send it
	checkDoneMsg struct{}
	tickMsg      time.Time
)

// Model is the bubbletea model for sshup.
type Model struct {
	hosts     []config.Host
	results   map[string]checker.Result
	order     []string
	timeout   time.Duration
	interval  time.Duration
	nextCheck time.Time
	checking  bool
	resultCh  <-chan checker.Result
	selected  int
	lastCheck time.Time
}

// New creates a Model with all hosts in pending state.
func New(hosts []config.Host, timeout, interval time.Duration) Model {
	order := make([]string, len(hosts))
	results := make(map[string]checker.Result, len(hosts))
	for i, h := range hosts {
		order[i] = h.Alias
		results[h.Alias] = checker.Result{Alias: h.Alias, Status: checker.StatusPending}
	}
	return Model{
		hosts:    hosts,
		results:  results,
		order:    order,
		timeout:  timeout,
		interval: interval,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(startCheckCmd(m.hosts, m.timeout), tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if !m.checking {
				return m, startCheckCmd(m.hosts, m.timeout)
			}
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.order)-1 {
				m.selected++
			}
		}

	case startedMsg:
		m.resultCh = msg.ch
		m.checking = true
		for _, alias := range m.order {
			m.results[alias] = checker.Result{Alias: alias, Status: checker.StatusPending}
		}
		return m, waitForResultCmd(m.resultCh)

	case ResultMsg:
		r := checker.Result(msg)
		m.results[r.Alias] = r
		return m, waitForResultCmd(m.resultCh)

	case checkDoneMsg:
		m.checking = false
		m.lastCheck = time.Now()
		m.nextCheck = time.Now().Add(m.interval)
		return m, tickCmd()

	case tickMsg:
		if !m.checking && m.interval > 0 && time.Now().After(m.nextCheck) {
			return m, startCheckCmd(m.hosts, m.timeout)
		}
		return m, tickCmd()

	case tea.WindowSizeMsg:
		// reserved for future responsive layout
	}

	return m, nil
}

const divider = "─────────────────────────────────────────────────────────────────────────────"

func (m Model) View() string {
	var sb strings.Builder

	// Header
	up := 0
	for _, r := range m.results {
		if r.Status == checker.StatusUp {
			up++
		}
	}
	total := len(m.order)

	header := fmt.Sprintf("sshup — %d hosts  ·  %d up", total, up)
	if !m.lastCheck.IsZero() {
		header += fmt.Sprintf("  ·  checked %s", m.lastCheck.Format("15:04:05"))
	}
	if m.checking {
		header += "  ·  checking..."
	} else if m.interval > 0 && !m.nextCheck.IsZero() {
		remaining := time.Until(m.nextCheck).Round(time.Second)
		if remaining > 0 {
			header += fmt.Sprintf("  ·  next in %s", remaining)
		}
	}
	sb.WriteString(styleTitle.Render(header) + "\n")
	sb.WriteString(styleDivider.Render(divider) + "\n")

	// Column headers
	sb.WriteString(styleColHead.Render(fmt.Sprintf(
		"%-22s %-10s %-20s %-8s %-12s %-6s",
		"HOST", "STATUS", "UPTIME", "LOAD", "MEM", "DISK",
	)) + "\n")
	sb.WriteString(styleDivider.Render(divider) + "\n")

	// Rows
	for i, alias := range m.order {
		r := m.results[alias]
		row := formatRow(r)
		if i == m.selected {
			sb.WriteString(styleSelected.Render(row) + "\n")
		} else {
			sb.WriteString(row + "\n")
		}
	}

	sb.WriteString(styleDivider.Render(divider) + "\n")
	sb.WriteString(styleHelp.Render("[r] refresh · [q] quit · [↑/↓] navigate") + "\n")

	return sb.String()
}

func formatRow(r checker.Result) string {
	statusStr := r.Status.String()
	styledStatus := statusStyle(statusStr).Render(statusStr)
	// Pad to 10 visible chars (ANSI codes are invisible; pad on plain string length)
	statusPad := strings.Repeat(" ", max(0, 10-len(statusStr)))

	const dash = "—"

	uptime := dash
	if r.Uptime != "" {
		uptime = r.Uptime
	}

	load := dash
	mem := dash
	disk := dash

	if r.Status == checker.StatusUp {
		load = fmt.Sprintf("%.2f", r.Load)

		if r.MemTotal > 0 {
			mem = fmt.Sprintf("%d/%d MB", r.MemUsed, r.MemTotal)
		} else {
			mem = "n/a"
		}

		if r.DiskPct > 0 {
			disk = fmt.Sprintf("%d%%", r.DiskPct)
		} else {
			disk = "n/a"
		}
	}

	return fmt.Sprintf("%-22s %s%s%-20s %-8s %-12s %-6s",
		truncate(r.Alias, 22),
		styledStatus,
		statusPad,
		truncate(uptime, 20),
		load,
		mem,
		disk,
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// Commands

func startCheckCmd(hosts []config.Host, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		return startedMsg{ch: checker.Check(hosts, timeout)}
	}
}

func waitForResultCmd(ch <-chan checker.Result) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return checkDoneMsg{}
		}
		return ResultMsg(r)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
