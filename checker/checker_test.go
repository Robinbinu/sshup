package checker

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
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
