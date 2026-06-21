package checker

import (
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sshup/sshup/config"
	"golang.org/x/crypto/ssh"
)

func TestParseMetrics(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		wantUptime   string
		wantLoad     float64
		wantMemUsed  int
		wantMemTotal int
		wantDiskPct  int
	}{
		{
			name:         "multi-day uptime output",
			output:       " 10:30:02 up 3 days,  2:49,  0 users,  load average: 1.25, 1.30, 1.35\n8008 578\n62%\n",
			wantUptime:   "3 days,  2:49",
			wantLoad:     1.25,
			wantMemUsed:  578,
			wantMemTotal: 8008,
			wantDiskPct:  62,
		},
		{
			name:         "sub-day uptime",
			output:       " 12:00:00 up 11:40,  0 users,  load average: 0.00, 0.01, 0.05\n1024 256\n45%\n",
			wantUptime:   "11:40",
			wantLoad:     0.00,
			wantMemUsed:  256,
			wantMemTotal: 1024,
			wantDiskPct:  45,
		},
		{
			name:         "no free output macOS remote",
			output:       " 10:00:00 up 10 days,  3:00,  2 users,  load averages: 0.50, 0.40, 0.30\n\n30%\n",
			wantUptime:   "10 days,  3:00",
			wantLoad:     0.50,
			wantMemUsed:  0,
			wantMemTotal: 0,
			wantDiskPct:  30,
		},
		{
			name:         "minutes uptime",
			output:       " 09:05:00 up 5 min,  1 user,  load average: 0.01, 0.05, 0.00\n2048 100\n10%\n",
			wantUptime:   "5 min",
			wantLoad:     0.01,
			wantMemUsed:  100,
			wantMemTotal: 2048,
			wantDiskPct:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uptime, load, memUsed, memTotal, diskPct := ParseMetrics(tt.output)

			if uptime != tt.wantUptime {
				t.Fatalf("uptime = %q, want %q", uptime, tt.wantUptime)
			}
			if load != tt.wantLoad {
				t.Fatalf("load = %v, want %v", load, tt.wantLoad)
			}
			if memUsed != tt.wantMemUsed {
				t.Fatalf("memUsed = %d, want %d", memUsed, tt.wantMemUsed)
			}
			if memTotal != tt.wantMemTotal {
				t.Fatalf("memTotal = %d, want %d", memTotal, tt.wantMemTotal)
			}
			if diskPct != tt.wantDiskPct {
				t.Fatalf("diskPct = %d, want %d", diskPct, tt.wantDiskPct)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{status: StatusPending, want: "..."},
		{status: StatusUp, want: "UP"},
		{status: StatusDown, want: "DOWN"},
		{status: StatusAuthErr, want: "AUTH ERR"},
		{status: Status(99), want: "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Fatalf("Status(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestCheckHostOnceTimesOutDuringSSHHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen returned error: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	originalKnownHosts := knownHostsCallbackFunc
	originalAuthSetup := authSetupFunc
	t.Cleanup(func() {
		knownHostsCallbackFunc = originalKnownHosts
		authSetupFunc = originalAuthSetup
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})

	knownHostsCallbackFunc = func(hostDeadline) (ssh.HostKeyCallback, error) {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	authSetupFunc = func(config.Host, hostDeadline) (authSetupResult, error) {
		return authSetupResult{methods: []ssh.AuthMethod{ssh.Password("unused")}}, nil
	}

	hostName, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort returned error: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi returned error: %v", err)
	}

	start := time.Now()
	result := checkHost(config.Host{Alias: "slow-host", HostName: hostName, Port: port}, 10*time.Millisecond)
	elapsed := time.Since(start)

	if result.Status != StatusDown {
		t.Fatalf("Status = %s, want %s", result.Status, StatusDown)
	}
	if !strings.Contains(result.Err, "host check timed out after 10ms") {
		t.Fatalf("Err = %q, want host check timeout", result.Err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want under 500ms", elapsed)
	}
}

func TestCheckHostDeadlineStartsBeforeAuthSetup(t *testing.T) {
	originalKnownHosts := knownHostsCallbackFunc
	originalAuthSetup := authSetupFunc
	t.Cleanup(func() {
		knownHostsCallbackFunc = originalKnownHosts
		authSetupFunc = originalAuthSetup
	})

	knownHostsCallbackFunc = func(hostDeadline) (ssh.HostKeyCallback, error) {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	authSetupFunc = func(config.Host, hostDeadline) (authSetupResult, error) {
		time.Sleep(20 * time.Millisecond)
		return authSetupResult{methods: []ssh.AuthMethod{ssh.Password("unused")}}, nil
	}

	start := time.Now()
	result := checkHost(config.Host{Alias: "auth-slow", HostName: "127.0.0.1", Port: 1}, 10*time.Millisecond)
	elapsed := time.Since(start)

	if result.Status != StatusDown {
		t.Fatalf("Status = %s, want %s", result.Status, StatusDown)
	}
	if !strings.Contains(result.Err, "host check timed out after 10ms") {
		t.Fatalf("Err = %q, want host check timeout", result.Err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want under 500ms", elapsed)
	}
}

func TestCheckHostTimesOutDuringKnownHostsSetup(t *testing.T) {
	originalKnownHosts := knownHostsCallbackFunc
	originalAuthSetup := authSetupFunc
	t.Cleanup(func() {
		knownHostsCallbackFunc = originalKnownHosts
		authSetupFunc = originalAuthSetup
	})

	knownHostsCallbackFunc = func(hostDeadline) (ssh.HostKeyCallback, error) {
		time.Sleep(time.Hour)
		return ssh.InsecureIgnoreHostKey(), nil
	}
	authSetupFunc = func(config.Host, hostDeadline) (authSetupResult, error) {
		return authSetupResult{methods: []ssh.AuthMethod{ssh.Password("unused")}}, nil
	}

	start := time.Now()
	result := checkHost(config.Host{Alias: "known-hosts-slow", HostName: "127.0.0.1", Port: 1}, 10*time.Millisecond)
	elapsed := time.Since(start)

	if result.Status != StatusDown {
		t.Fatalf("Status = %s, want %s", result.Status, StatusDown)
	}
	if !strings.Contains(result.Err, "during known_hosts setup") {
		t.Fatalf("Err = %q, want known_hosts setup timeout", result.Err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want under 500ms", elapsed)
	}
}

func TestAgentSignerProviderAppliesDeadlineToSigners(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	deadline := newHostDeadline(10 * time.Millisecond)
	start := time.Now()
	_, err := agentSignersFromConn(clientConn, deadline)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("agentSignersFromConn returned nil error, want timeout")
	}
	if !strings.Contains(err.Error(), "host check timed out after 10ms") {
		t.Fatalf("error = %q, want host check timeout", err.Error())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want under 500ms", elapsed)
	}
}

func TestCollectSignersCombinesFileAndAgentSigners(t *testing.T) {
	fileSigner := fakeSigner{name: "file"}
	agentSigner := fakeSigner{name: "agent"}

	signers, err := collectSignersFromProviders(
		func() ([]ssh.Signer, error) {
			return []ssh.Signer{fileSigner}, nil
		},
		func() ([]ssh.Signer, error) {
			return []ssh.Signer{agentSigner}, nil
		},
	)
	if err != nil {
		t.Fatalf("collectSignersFromProviders returned error: %v", err)
	}
	if len(signers) != 2 {
		t.Fatalf("len(signers) = %d, want 2", len(signers))
	}
	if signers[0] != fileSigner || signers[1] != agentSigner {
		t.Fatalf("signers = %#v, want file then agent", signers)
	}
}

func TestCollectSignersSkipsUnavailableProviders(t *testing.T) {
	signers, err := collectSignersFromProviders(
		func() ([]ssh.Signer, error) {
			return nil, errors.New("missing key")
		},
		func() ([]ssh.Signer, error) {
			return []ssh.Signer{fakeSigner{name: "agent"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("collectSignersFromProviders returned error: %v", err)
	}
	if len(signers) != 1 {
		t.Fatalf("len(signers) = %d, want 1", len(signers))
	}
}

func TestApplyConnDeadlineSetsDeadlineWhenTimeoutPositive(t *testing.T) {
	conn := &fakeNetConn{}
	deadline := newHostDeadline(time.Second)

	if err := applyConnDeadline(conn, deadline); err != nil {
		t.Fatalf("applyConnDeadline returned error: %v", err)
	}
	if conn.deadline.IsZero() {
		t.Fatal("deadline was not set")
	}
}

func TestApplyConnDeadlineSkipsDeadlineWhenTimeoutNonPositive(t *testing.T) {
	conn := &fakeNetConn{}

	if err := applyConnDeadline(conn, newHostDeadline(0)); err != nil {
		t.Fatalf("applyConnDeadline returned error: %v", err)
	}
	if !conn.deadline.IsZero() {
		t.Fatalf("deadline = %v, want zero", conn.deadline)
	}
}

func TestRunRemoteCommandReturnsOutput(t *testing.T) {
	session := newFakeCommandSession(0, []byte("ok\n"), nil)

	output, err := runRemoteCommand(session, "uptime", time.Second)
	if err != nil {
		t.Fatalf("runRemoteCommand returned error: %v", err)
	}
	if string(output) != "ok\n" {
		t.Fatalf("output = %q, want %q", string(output), "ok\n")
	}
	if session.wasClosed() {
		t.Fatal("session was closed on successful command")
	}
}

func TestRunRemoteCommandTimesOutAndClosesSession(t *testing.T) {
	session := newFakeCommandSession(time.Hour, nil, nil)

	start := time.Now()
	_, err := runRemoteCommand(session, "uptime", 10*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runRemoteCommand returned nil error, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q, want timeout message", err.Error())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want under 500ms", elapsed)
	}
	if !session.wasClosed() {
		t.Fatal("session was not closed after timeout")
	}
}

type fakeCommandSession struct {
	delay    time.Duration
	output   []byte
	err      error
	closeCh  chan struct{}
	closeErr error

	mu     sync.Mutex
	closed bool
}

func newFakeCommandSession(delay time.Duration, output []byte, err error) *fakeCommandSession {
	return &fakeCommandSession{
		delay:   delay,
		output:  output,
		err:     err,
		closeCh: make(chan struct{}),
	}
}

func (s *fakeCommandSession) Output(string) ([]byte, error) {
	select {
	case <-time.After(s.delay):
		return s.output, s.err
	case <-s.closeCh:
		return nil, errors.New("session closed")
	}
}

func (s *fakeCommandSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		close(s.closeCh)
		s.closed = true
	}

	return s.closeErr
}

func (s *fakeCommandSession) wasClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

type fakeSigner struct {
	name string
}

func (s fakeSigner) PublicKey() ssh.PublicKey {
	return nil
}

func (s fakeSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return &ssh.Signature{}, nil
}

type fakeNetConn struct {
	deadline time.Time
}

func (c *fakeNetConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *fakeNetConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *fakeNetConn) Close() error {
	return nil
}

func (c *fakeNetConn) LocalAddr() net.Addr {
	return nil
}

func (c *fakeNetConn) RemoteAddr() net.Addr {
	return nil
}

func (c *fakeNetConn) SetDeadline(t time.Time) error {
	c.deadline = t
	return nil
}

func (c *fakeNetConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *fakeNetConn) SetWriteDeadline(time.Time) error {
	return nil
}
