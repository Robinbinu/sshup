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

var checkHostOnceFunc = checkHostOnce

func checkHost(host config.Host, timeout time.Duration) Result {
	checkOnce := checkHostOnceFunc
	if timeout <= 0 {
		return checkOnce(host, timeout)
	}

	results := make(chan Result, 1)
	go func() {
		results <- checkOnce(host, timeout)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-results:
		return result
	case <-timer.C:
		return Result{
			Alias:  host.Alias,
			Status: StatusDown,
			Err:    fmt.Sprintf("host check timed out after %s", timeout),
		}
	}
}

func checkHostOnce(host config.Host, timeout time.Duration) Result {
	result := Result{
		Alias:  host.Alias,
		Status: StatusDown,
	}

	hostKeyCallback, err := knownHostsCallback()
	if err != nil {
		result.Err = err.Error()
		return result
	}

	methods, err := authMethods(host)
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

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", hostName, port), clientConfig)
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

	output, err := runRemoteCommand(session, remoteCmd, timeout, client)
	if err != nil {
		result.Err = err.Error()
		return result
	}

	result.Uptime, result.Load, result.MemUsed, result.MemTotal, result.DiskPct = ParseMetrics(string(output))
	result.Status = StatusUp
	return result
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

func authMethods(host config.Host) ([]ssh.AuthMethod, error) {
	paths := identityPaths(host.IdentityFile)
	var methods []ssh.AuthMethod

	for _, path := range paths {
		key, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if agentMethod, ok := agentAuthMethod(); ok {
		methods = append(methods, agentMethod)
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no usable SSH keys found")
	}

	return methods, nil
}

func agentAuthMethod() (ssh.AuthMethod, bool) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if strings.TrimSpace(sock) == "" {
		return nil, false
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, false
	}

	agentClient := agent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), true
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
