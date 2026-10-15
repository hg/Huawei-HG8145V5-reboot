package main

import (
	"context"
	"os/exec"
	"time"
)

// Pinger reports whether host answers a single ping. Production uses the
// system ping binary (like reconnect.sh's ping -c1 -W2); tests stub it.
type Pinger interface {
	Ping(ctx context.Context, host string) bool
}

// ExecPinger shells out to `ping -c1 -W2 host`.
type ExecPinger struct {
	// Path, when set, overrides the ping binary lookup (tests).
	Path string
}

// Ping runs ping with a context timeout guard.
func (p ExecPinger) Ping(ctx context.Context, host string) bool {
	bin := p.Path
	if bin == "" {
		bin = "ping"
	}
	// -W2 mirrors the shell's per-packet timeout; bound the whole thing too.
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, bin, "-c1", "-W2", host)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// SequencePinger replays a scripted up/down sequence for tests.
// Each Ping consumes the next entry; once exhausted it repeats the last.
type SequencePinger struct {
	Seq   []bool
	n     int
	calls int
}

// Ping implements Pinger.
func (s *SequencePinger) Ping(_ context.Context, _ string) bool {
	s.calls++
	if len(s.Seq) == 0 {
		return true
	}
	if s.n >= len(s.Seq) {
		return s.Seq[len(s.Seq)-1]
	}
	v := s.Seq[s.n]
	s.n++
	return v
}

// Calls returns how many pings were attempted.
func (s *SequencePinger) Calls() int { return s.calls }

// FuncPinger adapts a func to Pinger.
type FuncPinger func(ctx context.Context, host string) bool

// Ping implements Pinger.
func (f FuncPinger) Ping(ctx context.Context, host string) bool { return f(ctx, host) }
