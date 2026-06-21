package checker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sshup/sshup/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const remoteCmd = "uptime; free -m 2>/dev/null | awk '/Mem:/{print $2,$3}'; df / 2>/dev/null | awk 'NR==2{print $5}'"

type Status int

const (
	StatusPending Status = iota
	StatusUp
	StatusDown
	StatusAuthErr
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "..."
	case StatusUp:
		return "UP"
	case StatusDown:
		return "DOWN"
	case StatusAuthErr:
		return "AUTH ERR"
	default:
		return "UNKNOWN"
	}
}

type Result struct {
	Alias    string
	Status   Status
	Uptime   string
	Load     float64
	MemUsed  int
	MemTotal int
	DiskPct  int
	Err      string
}

func Check(hosts []config.Host, timeout time.Duration) <-chan Result {
	results := make(chan Result)

	go func() {
		var wg sync.WaitGroup
		wg.Add(len(hosts))

		for _, host := range hosts {
			host := host
			go func() {
				defer wg.Done()
				results <- checkHost(host, timeout)
			}()
		}

		wg.Wait()
		close(results)
	}()

	return results
}

func checkHost(host config.Host, timeout time.Duration) Result {
	return checkHostOnce(host, timeout)
}

func checkHostOnce(host config.Host, timeout time.Duration) Result {
	result := Result{
		Alias:  host.Alias,
		Status: StatusDown,
	}

	hostKeyCallback, err := knownHostsCallbackFunc()
	if err != nil {
		result.Err = err.Error()
		return result
	}

	methods, err := authMethodsFunc(host)
	if err != nil {
		result.Status = StatusAuthErr
		result.Err = err.Error()
		return result
	}

	clientConfig := &ssh.ClientConfig{
		User:            host.User,
		Auth:            methods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}

	hostName := host.HostName
	if hostName == "" {
		hostName = host.Alias
	}
	port := host.Port
	if port == 0 {
		port = 22
	}

	addr := fmt.Sprintf("%s:%d", hostName, port)
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	client, err := dialSSHClient(addr, clientConfig, timeout, deadline)
	if err != nil {
		if isAuthError(err) {
			result.Status = StatusAuthErr
		}
		result.Err = err.Error()
		return result
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		result.Err = err.Error()
		return result
	}
	defer session.Close()

	output, err := runRemoteCommand(session, remoteCmd, remainingTimeout(deadline, timeout), client)
	if err != nil {
		result.Err = err.Error()
		return result
	}

	result.Uptime, result.Load, result.MemUsed, result.MemTotal, result.DiskPct = ParseMetrics(string(output))
	result.Status = StatusUp
	return result
}

func dialSSHClient(addr string, clientConfig *ssh.ClientConfig, timeout time.Duration, deadline time.Time) (*ssh.Client, error) {
	conn, err := dialTCP(addr, timeout)
	if err != nil {
		return nil, hostCheckError(timeout, err)
	}

	if err := applyConnDeadline(conn, deadline, timeout); err != nil {
		_ = conn.Close()
		return nil, hostCheckError(timeout, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()
		return nil, hostCheckError(timeout, err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func hostCheckError(timeout time.Duration, err error) error {
	if timeout > 0 && (os.IsTimeout(err) || strings.Contains(err.Error(), "i/o timeout")) {
		return fmt.Errorf("host check timed out after %s: %w", timeout, err)
	}
	return err
}

func dialTCP(addr string, timeout time.Duration) (net.Conn, error) {
	if timeout > 0 {
		return net.DialTimeout("tcp", addr, timeout)
	}
	return net.Dial("tcp", addr)
}

func applyConnDeadline(conn net.Conn, deadline time.Time, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	return conn.SetDeadline(deadline)
}

func remainingTimeout(deadline time.Time, fallback time.Duration) time.Duration {
	if fallback <= 0 || deadline.IsZero() {
		return fallback
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}

	return remaining
}

func ParseMetrics(output string) (uptime string, load float64, memUsed, memTotal, diskPct int) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return "", 0, 0, 0, 0
	}

	uptime, load = parseUptimeLine(lines[0])

	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 2 && memTotal == 0 && memUsed == 0 {
			total, totalErr := strconv.Atoi(fields[0])
			used, usedErr := strconv.Atoi(fields[1])
			if totalErr == nil && usedErr == nil {
				memTotal = total
				memUsed = used
				continue
			}
		}

		for _, field := range fields {
			if strings.HasSuffix(field, "%") {
				pct, err := strconv.Atoi(strings.TrimSuffix(field, "%"))
				if err == nil {
					diskPct = pct
				}
			}
		}
	}

	return uptime, load, memUsed, memTotal, diskPct
}

type commandSession interface {
	Output(string) ([]byte, error)
	Close() error
}

type closer interface {
	Close() error
}

func runRemoteCommand(session commandSession, cmd string, timeout time.Duration, extraClosers ...closer) ([]byte, error) {
	if timeout <= 0 {
		return session.Output(cmd)
	}

	type commandResult struct {
		output []byte
		err    error
	}

	done := make(chan commandResult, 1)
	go func() {
		output, err := session.Output(cmd)
		done <- commandResult{output: output, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-done:
		return result.output, result.err
	case <-timer.C:
		_ = session.Close()
		for _, closer := range extraClosers {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("remote command timed out after %s", timeout)
	}
}

func parseUptimeLine(line string) (string, float64) {
	re := regexp.MustCompile(`\bup\s+(.+),\s+\d+\s+users?,\s+load averages?:\s*([0-9]+(?:\.[0-9]+)?)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) != 3 {
		return "", 0
	}

	load, err := strconv.ParseFloat(matches[2], 64)
	if err != nil {
		return strings.TrimSpace(matches[1]), 0
	}

	return strings.TrimSpace(matches[1]), load
}

var authMethodsFunc = authMethods
var knownHostsCallbackFunc = knownHostsCallback

type signerProvider func() ([]ssh.Signer, error)

func authMethods(host config.Host) ([]ssh.AuthMethod, error) {
	signers, err := collectSigners(host)
	if err != nil {
		return nil, err
	}

	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil
}

func collectSigners(host config.Host) ([]ssh.Signer, error) {
	return collectSignersFromProviders(fileSignerProvider(host), agentSignerProvider)
}

func collectSignersFromProviders(providers ...signerProvider) ([]ssh.Signer, error) {
	var signers []ssh.Signer

	for _, provider := range providers {
		providerSigners, err := provider()
		if err != nil {
			continue
		}
		signers = append(signers, providerSigners...)
	}

	if len(signers) == 0 {
		return nil, fmt.Errorf("no usable SSH keys found")
	}

	return signers, nil
}

func fileSignerProvider(host config.Host) signerProvider {
	return func() ([]ssh.Signer, error) {
		paths := identityPaths(host.IdentityFile)
		var signers []ssh.Signer

		for _, path := range paths {
			key, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				continue
			}
			signers = append(signers, signer)
		}

		if len(signers) == 0 {
			return nil, fmt.Errorf("no usable key files found")
		}

		return signers, nil
	}
}

func agentSignerProvider() ([]ssh.Signer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if strings.TrimSpace(sock) == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if len(signers) == 0 {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH agent has no signers")
	}

	return signers, nil
}

func knownHostsCallback() (ssh.HostKeyCallback, error) {
	path := expandHome("~/.ssh/known_hosts")
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("known_hosts verification unavailable at %s: %w", path, err)
	}

	return callback, nil
}

func identityPaths(identityFile string) []string {
	if strings.TrimSpace(identityFile) != "" {
		return []string{expandHome(identityFile)}
	}

	return []string{
		expandHome("~/.ssh/id_ed25519"),
		expandHome("~/.ssh/id_ecdsa"),
		expandHome("~/.ssh/id_rsa"),
	}
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func isAuthError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unable to authenticate") ||
		strings.Contains(message, "permission denied") ||
		strings.Contains(message, "no supported methods remain")
}
