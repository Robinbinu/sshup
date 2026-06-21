# sshup

Live SSH server health monitor. Reads `~/.ssh/config` and checks all hosts in parallel.

## Install

```bash
go install github.com/sshup/sshup@latest
```

## Usage

```bash
sshup                          # uses ~/.ssh/config, refreshes every 30s
sshup --interval 60            # refresh every 60s
sshup --interval 0             # disable auto-refresh (manual r key only)
sshup --timeout 5              # 5s connection timeout per host
sshup --config ~/work/ssh      # alternate SSH config
```

## Keys

| Key | Action |
|-----|--------|
| `r` | Force refresh |
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `q` / `ctrl+c` | Quit |

## What it shows

| Column | Description |
|--------|-------------|
| HOST | SSH config alias |
| STATUS | UP / DOWN / AUTH ERR |
| UPTIME | Time since last reboot |
| LOAD | 1-minute load average |
| MEM | Used / total RAM (MB) |
| DISK | Root partition used % |

## How it works

`sshup` reads all `Host` blocks from your SSH config (skipping wildcards), opens parallel SSH connections, and runs a single compound command on each remote:

```bash
uptime; free -m 2>/dev/null | awk '/Mem:/{print $2,$3}'; df / 2>/dev/null | awk 'NR==2{print $5}'
```

Results update the TUI row-by-row as they arrive. No agent, no daemon, no config file beyond your existing `~/.ssh/config`.
