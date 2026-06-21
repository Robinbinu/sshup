# sshup

**Live SSH server health in your terminal — zero config, just `sshup`.**

```
sshup — 9 hosts  ·  9 up  ·  checked 14:32:01  ·  next in 28s
─────────────────────────────────────────────────────────────────────────────
HOST                    STATUS   UPTIME             LOAD    MEM           DISK
192.0.2.10          UP       3d 2h              1.25    578/8008 MB   62%
192.0.2.11           UP       919d 15h           2.22    4221/7629 MB  45%
192.0.2.12           UP       117d 1h            1.83    3100/7813 MB  78%
192.0.2.13            UP       382d 18h           0.07    512/8192 MB   30%
192.0.2.14         UP       348d 4h            0.09    800/8008 MB   55%
192.0.2.15          UP       13d 1h             10.63   6200/7629 MB  88%
192.0.2.16          UP       11h 50m            0.01    256/8008 MB   12%
192.0.2.17           UP       398d 6h            0.21    1200/8008 MB  41%
aws-xwch                DOWN     —                  —       —             —
─────────────────────────────────────────────────────────────────────────────
[r] refresh · [q] quit · [↑/↓] navigate
```

`sshup` reads your existing `~/.ssh/config`, checks every host in parallel over SSH, and shows uptime, load, memory, and disk — live, as results come in. Rows update one by one as checks complete. No daemon. No config file. No setup. If it's in your SSH config, it's in `sshup`.

---

## Install

**Homebrew / Go install:**
```bash
go install github.com/Robinbinu/sshup@latest
```

**Prebuilt binary** — download from [Releases](https://github.com/Robinbinu/sshup/releases):

| Platform | Download |
|----------|----------|
| macOS (Apple Silicon) | `sshup_darwin_arm64` |
| macOS (Intel) | `sshup_darwin_amd64` |
| Linux x86-64 | `sshup_linux_amd64` |
| Linux ARM64 | `sshup_linux_arm64` |
| Windows x86-64 | `sshup_windows_amd64.exe` |
| Windows ARM64 | `sshup_windows_arm64.exe` |

```bash
# macOS / Linux — make executable and move to PATH
chmod +x sshup_* && sudo mv sshup_* /usr/local/bin/sshup
```

---

## Why sshup?

You manage servers. You open a terminal and `ssh` into each one to check if it's alive, what the load is, whether disk is filling up. You do this every morning. You do it before deploys. You do it when something feels slow.

`sshup` does it all at once, in parallel, in a single terminal window that stays live.

It works with whatever SSH setup you already have — your `~/.ssh/config` is the source of truth. No new credentials, no agent to install, no YAML to write.

---

## Usage

```bash
sshup                        # check all hosts in ~/.ssh/config, refresh every 30s
sshup --interval 60          # refresh every 60s
sshup --interval 0           # manual refresh only (press r)
sshup --timeout 5            # 5s connection timeout per host
sshup --config ~/work/.ssh   # use a different SSH config
```

---

## Keys

| Key | Action |
|-----|--------|
| `r` | Force refresh now |
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `q` / `ctrl+c` | Quit |

---

## What it shows

| Column | Description |
|--------|-------------|
| HOST | SSH config alias |
| STATUS | `UP` · `DOWN` · `AUTH ERR` |
| UPTIME | Time since last reboot |
| LOAD | 1-minute load average |
| MEM | Used / total RAM |
| DISK | Root partition used % |

Rows arrive live — fast hosts appear immediately, slow or unreachable ones fill in as their checks complete or time out.

---

## How it works

`sshup` reads all `Host` blocks from your `~/.ssh/config` (skipping wildcards), opens one SSH connection per host in parallel, and runs a single compound command on each:

```bash
uptime; free -m 2>/dev/null | awk '/Mem:/{print $2,$3}'; df / 2>/dev/null | awk 'NR==2{print $5}'
```

Auth uses your SSH agent (if running) plus any `IdentityFile` keys from your config, with fallback to `~/.ssh/id_ed25519`, `~/.ssh/id_ecdsa`, and `~/.ssh/id_rsa`. Host verification uses `~/.ssh/known_hosts`.

`free` is Linux-only — memory shows `n/a` on macOS or BSD remotes, everything else still works.

---

## Requirements

- Go 1.22+ (for `go install`)
- SSH key auth configured for your hosts
- Hosts in `~/.ssh/known_hosts` (first-time SSH users: run `ssh <host>` once to add them)

---

## Contributing

PRs welcome. Run `go test ./... -race` before submitting.

---

## License

MIT — see [LICENSE](LICENSE)
