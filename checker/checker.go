package checker

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Robinbinu/sshup/config"
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
	deadline := newHostDeadline(timeout)

	knownHostsCallback := knownHostsCallbackFunc
	hostKeyCallback, err := runWithDeadline(deadline, "known_hosts setup", func() (ssh.HostKeyCallback, error) {
		return knownHostsCallback(deadline)
	})
	if err != nil {
		result.Err = err.Error()
		return result
	}
	if err := deadline.check(); err != nil {
		result.Err = err.Error()
		return result
	}

	authSetup := authSetupFunc
	auth, err := runWithDeadline(deadline, "auth setup", func() (authSetupResult, error) {
		return authSetup(host, deadline)
	})
	if err != nil {
		if !deadline.isTimeout(err) {
			result.Status = StatusAuthErr
		}
		result.Err = err.Error()
		return result
	}
	defer closeAll(auth.closers)
	if err := deadline.check(); err != nil {
		result.Err = err.Error()
		return result
	}

	clientConfig := &ssh.ClientConfig{
		User:            host.User,
		Auth:            auth.methods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         deadline.remaining(),
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

	client, err := dialSSHClient(addr, clientConfig, deadline)
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
		result.Err = deadline.wrap(err).Error()
		return result
	}
	defer session.Close()

	output, err := runRemoteCommand(session, remoteCmd, deadline.remaining(), client)
	if err != nil {
		result.Err = deadline.wrap(err).Error()
		return result
	}

	result.Uptime, result.Load, result.MemUsed, result.MemTotal, result.DiskPct = ParseMetrics(string(output))
	result.Status = StatusUp
	return result
}

type hostDeadline struct {
	timeout  time.Duration
	deadline time.Time
}

func newHostDeadline(timeout time.Duration) hostDeadline {
	deadline := hostDeadline{timeout: timeout}
	if timeout > 0 {
		deadline.deadline = time.Now().Add(timeout)
	}
	return deadline
}

func (d hostDeadline) active() bool {
	return d.timeout > 0
}

func (d hostDeadline) remaining() time.Duration {
	if !d.active() {
		return 0
	}

	remaining := time.Until(d.deadline)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func (d hostDeadline) check() error {
	if d.active() && d.remaining() <= 0 {
		return d.timeoutError()
	}
	return nil
}

func (d hostDeadline) timeoutError() error {
	return fmt.Errorf("host check timed out after %s", d.timeout)
}

func (d hostDeadline) wrap(err error) error {
	if err == nil {
		return nil
	}
	if d.active() && (d.remaining() <= 0 || d.isTimeout(err)) {
		return fmt.Errorf("host check timed out after %s: %w", d.timeout, err)
	}
	return err
}

func (d hostDeadline) isTimeout(err error) bool {
	return d.active() && err != nil && (os.IsTimeout(err) ||
		strings.Contains(err.Error(), "i/o timeout") ||
		strings.Contains(err.Error(), "host check timed out after"))
}

func runWithDeadline[T any](deadline hostDeadline, label string, fn func() (T, error)) (T, error) {
	var zero T
	if !deadline.active() {
		return fn()
	}
	if err := deadline.check(); err != nil {
		return zero, err
	}

	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := fn()
		done <- result{value: value, err: err}
	}()

	timer := time.NewTimer(deadline.remaining())
	defer timer.Stop()

	select {
	case result := <-done:
		return result.value, result.err
	case <-timer.C:
		return zero, fmt.Errorf("host check timed out after %s during %s", deadline.timeout, label)
	}
}

func dialSSHClient(addr string, clientConfig *ssh.ClientConfig, deadline hostDeadline) (*ssh.Client, error) {
	conn, err := dialTCP(addr, deadline)
	if err != nil {
		return nil, deadline.wrap(err)
	}

	if err := applyConnDeadline(conn, deadline); err != nil {
		_ = conn.Close()
		return nil, deadline.wrap(err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()
		return nil, deadline.wrap(err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func dialTCP(addr string, deadline hostDeadline) (net.Conn, error) {
	if deadline.active() {
		if err := deadline.check(); err != nil {
			return nil, err
		}
		return net.DialTimeout("tcp", addr, deadline.remaining())
	}
	return net.Dial("tcp", addr)
}

func applyConnDeadline(conn net.Conn, deadline hostDeadline) error {
	if !deadline.active() {
		return nil
	}
	if err := deadline.check(); err != nil {
		return err
	}
	return conn.SetDeadline(deadline.deadline)
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

var authSetupFunc = authSetup
var knownHostsCallbackFunc = knownHostsCallback

type signerProvider func() ([]ssh.Signer, error)

type authSetupResult struct {
	methods []ssh.AuthMethod
	closers []io.Closer
}

func authSetup(host config.Host, deadline hostDeadline) (authSetupResult, error) {
	signers, closers, err := collectSigners(host, deadline)
	if err != nil {
		closeAll(closers)
		return authSetupResult{}, err
	}

	return authSetupResult{
		methods: []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		closers: closers,
	}, nil
}

func collectSigners(host config.Host, deadline hostDeadline) ([]ssh.Signer, []io.Closer, error) {
	// Establish the agent connection up front so we can track its closer.
	agentSigners, agentConn, agentErr := agentSignerProvider(deadline)

	var closers []io.Closer
	if agentConn != nil {
		closers = append(closers, agentConn)
	}

	// Build provider list. Agent provider is only included when the connection
	// succeeded and returned signers.
	providers := []signerProvider{fileSignerProvider(host, deadline)}
	if agentErr == nil && len(agentSigners) > 0 {
		captured := agentSigners
		providers = append(providers, func() ([]ssh.Signer, error) {
			return captured, nil
		})
	}

	signers, err := collectSignersFromProviders(providers...)
	if err != nil {
		closeAll(closers)
		// Surface the most useful deadline error if either provider timed out.
		if deadline.isTimeout(agentErr) {
			return nil, nil, agentErr
		}
		return nil, nil, err
	}

	return signers, closers, nil
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

func fileSignerProvider(host config.Host, deadline hostDeadline) signerProvider {
	return func() ([]ssh.Signer, error) {
		paths := identityPaths(host.IdentityFiles)
		var signers []ssh.Signer

		for _, path := range paths {
			if err := deadline.check(); err != nil {
				return nil, err
			}
			key, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if err := deadline.check(); err != nil {
				return nil, err
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

func agentSignerProvider(deadline hostDeadline) ([]ssh.Signer, io.Closer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if strings.TrimSpace(sock) == "" {
		return nil, nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}

	conn, err := dialAgentSocket(sock, deadline)
	if err != nil {
		return nil, nil, err
	}

	signers, err := agentSignersFromConn(conn, deadline)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if len(signers) == 0 {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("SSH agent has no signers")
	}

	return signers, conn, nil
}

func dialAgentSocket(sock string, deadline hostDeadline) (net.Conn, error) {
	if deadline.active() {
		if err := deadline.check(); err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: deadline.remaining()}
		conn, err := dialer.Dial("unix", sock)
		if err != nil {
			return nil, deadline.wrap(err)
		}
		return conn, nil
	}

	return net.Dial("unix", sock)
}

func agentSignersFromConn(conn net.Conn, deadline hostDeadline) ([]ssh.Signer, error) {
	if err := applyConnDeadline(conn, deadline); err != nil {
		return nil, err
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		return nil, deadline.wrap(err)
	}
	return signers, nil
}

func closeAll(closers []io.Closer) {
	for _, closer := range closers {
		_ = closer.Close()
	}
}

func knownHostsCallback(hostDeadline) (ssh.HostKeyCallback, error) {
	path := expandHome("~/.ssh/known_hosts")
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("known_hosts verification unavailable at %s: %w", path, err)
	}

	return callback, nil
}

func identityPaths(identityFiles []string) []string {
	var specified []string
	for _, f := range identityFiles {
		if strings.TrimSpace(f) != "" {
			specified = append(specified, expandHome(f))
		}
	}
	if len(specified) > 0 {
		return specified
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
