package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sshup/sshup/config"
	"github.com/sshup/sshup/tui"
)

func main() {
	defaultConfig := os.ExpandEnv("$HOME/.ssh/config")

	configPath := flag.String("config", defaultConfig, "path to SSH config file")
	interval := flag.Int("interval", 30, "auto-refresh interval in seconds (0 to disable)")
	timeout := flag.Int("timeout", 10, "per-host SSH connection timeout in seconds")
	flag.Parse()

	hosts, err := config.Parse(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshup: failed to read %s: %v\n", *configPath, err)
		os.Exit(1)
	}
	if len(hosts) == 0 {
		fmt.Fprintf(os.Stderr, "sshup: no hosts found in %s\n", *configPath)
		os.Exit(1)
	}

	m := tui.New(
		hosts,
		time.Duration(*timeout)*time.Second,
		time.Duration(*interval)*time.Second,
	)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshup: %v\n", err)
		os.Exit(1)
	}
}
