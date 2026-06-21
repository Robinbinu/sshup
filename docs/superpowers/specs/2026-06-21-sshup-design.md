# sshup — Design Spec

**Date:** 2026-06-21  
**Status:** Approved

---

## Overview

`sshup` is a lightweight Go TUI that reads `~/.ssh/config`, checks all defined hosts in parallel over SSH, and displays a live status board with health metrics. Designed to be fast, reactive, dependency-free at runtime, and general — works for any SSH user without additional configuration.

---

## Goals

- Zero config: reads `~/.ssh/config` out of the box
- Single static binary, no runtime dependencies
- Live TUI that updates rows as results arrive (not batch)
- Auto-refreshes every 30s; manual refresh with `r`
- Works on any POSIX server (Linux/macOS) — metrics collected via standard shell commands

---

## Architecture

```
sshup/
├── main.go              # flags, wires config → checker → tui
├── config/
│   └── parser.go        # parses ~/.ssh/config → []Host
├── checker/
│   └── checker.go       # parallel SSH checks → []Result via channel
└── tui/
    ├── model.go          # bubbletea model: state, Update(), View()
    └── styles.go         # lipgloss colours, column widths, layout
```

### Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/charmbracelet/bubbletea` | TUI event loop |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/kevinburke/ssh_config` | SSH config parser |
| `golang.org/x/crypto/ssh` | SSH client |

---

## Config Parsing (`config/parser.go`)

- Reads the file at `--config` (default: `~/.ssh/config`)
- Extracts all `Host` blocks, skipping wildcards (`Host *`)
- Per host: alias, HostName, User (default: current OS user), Port (default: 22), IdentityFile
- Returns `[]Host` in the order they appear in the config

```go
type Host struct {
    Alias        string
    HostName     string
    User         string
    Port         int
    IdentityFile string
}
```

---

## SSH Checker (`checker/checker.go`)

Launches one goroutine per host. Each goroutine:

1. Opens SSH connection (respects `--timeout`, default 10s)
2. Runs a single compound command:
   ```bash
   uptime; free -m | awk '/Mem:/{print $2,$3}'; df / | awk 'NR==2{print $5}'
   ```
3. Parses stdout into a `Result`
4. Sends `Result` on a channel — TUI updates that row immediately

```go
type Result struct {
    Alias   string
    Status  Status   // Up | Down | AuthErr
    Uptime  string
    Load    float64
    MemUsed int
    MemTotal int
    DiskPct int
    Err     string   // populated on Down/AuthErr
}
```

Errors map as:
- Dial timeout / connection refused → `Down`
- Auth failure (`ssh: unable to authenticate`) → `AuthErr`
- Connected but command parse failure → `Up` with zero-value metrics

---

## TUI (`tui/model.go`)

### Layout

```
sshup — 9 hosts  ·  refreshed 14:32:01  ·  next in 28s
─────────────────────────────────────────────────────────────────────
HOST                  STATUS    UPTIME          LOAD    MEM      DISK
36.248.221.170        UP        3d 2h           1.25    45%      62%
47.105.71.219         UP        919d 15h        2.22    78%      45%
120.27.29.129         DOWN      —               —       —        —
aws-xwch              AUTH ERR  —               —       —        —
─────────────────────────────────────────────────────────────────────
[r] refresh · [q] quit · [↑/↓] navigate
```

### Colours

| State | Colour |
|-------|--------|
| UP | green |
| DOWN | red |
| AUTH ERR | yellow |
| UP (partial metrics) | dim white |
| Pending (checking...) | grey italic |

### Behaviour

- Rows render immediately on load showing `checking...` status
- Each row updates in place as its goroutine completes — no waiting for slowest host
- Header countdown ticks every second via `bubbletea.Tick`
- At interval=0: new check cycle fires, all rows reset to `checking...`
- `r` key triggers immediate re-check cycle
- `↑/↓` navigates rows (reserved for future detail panel)
- `q` / `ctrl+c` exits

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `~/.ssh/config` | Path to SSH config |
| `--interval` | `30` | Auto-refresh interval in seconds (0 = disable) |
| `--timeout` | `10` | Per-host SSH connection timeout in seconds |

---

## Error Handling

- SSH config not found → fatal error with clear message, exit 1
- No hosts found in config → show empty table with hint message
- Individual host failures → shown inline in the row, never crash the TUI
- `free` not available (macOS) → mem fields show `n/a`

---

## Out of Scope (v1)

- Detail panel / drill-down per host
- Alerting / notifications on state change
- Custom labels / groups in config
- Windows support
- Non-SSH checks (HTTP, ping)
