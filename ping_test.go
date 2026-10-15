package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSequencePinger(t *testing.T) {
	s := &SequencePinger{Seq: []bool{true, false}}
	if !s.Ping(context.Background(), "h") {
		t.Fatal("first should be true")
	}
	if s.Ping(context.Background(), "h") {
		t.Fatal("second should be false")
	}
	// Exhausted repeats last.
	if s.Ping(context.Background(), "h") {
		t.Fatal("should repeat false")
	}
	if s.Calls() != 3 {
		t.Fatalf("calls %d", s.Calls())
	}
	empty := &SequencePinger{}
	if !empty.Ping(context.Background(), "h") {
		t.Fatal("empty defaults true")
	}
}

func TestFuncPinger(t *testing.T) {
	var gotHost string
	p := FuncPinger(func(ctx context.Context, host string) bool {
		gotHost = host
		return true
	})
	if !p.Ping(context.Background(), "1.1.1.1") {
		t.Fatal("bad")
	}
	if gotHost != "1.1.1.1" {
		t.Fatalf("host %q", gotHost)
	}
}

func TestExecPinger_WithStubBinaries(t *testing.T) {
	// /bin/true ignores args and exits 0; /bin/false exits 1. No network used.
	up := ExecPinger{Path: "/bin/true"}
	if !up.Ping(context.Background(), "anything") {
		t.Fatal("true should be up")
	}
	down := ExecPinger{Path: "/bin/false"}
	if down.Ping(context.Background(), "anything") {
		t.Fatal("false should be down")
	}
	missing := ExecPinger{Path: "/nonexistent-binary-xyz"}
	if missing.Ping(context.Background(), "h") {
		t.Fatal("missing binary should be down")
	}
}

func TestWaitForDrop_Observed(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf)
	// up, up, down, down(final probe down) => dropped
	p := &SequencePinger{Seq: []bool{true, true, false, false}}
	cfg := testCfg()
	cfg.DropTimeout = 5 * time.Second
	cfg.PingInterval = time.Millisecond
	if !waitForDrop(context.Background(), cfg, p, func(time.Duration) {}, log) {
		t.Fatal("expected dropped")
	}
}

func TestWaitForDrop_NoDrop(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf)
	p := &SequencePinger{Seq: []bool{true}} // always up
	cfg := testCfg()
	cfg.DropTimeout = 20 * time.Millisecond
	cfg.PingInterval = time.Millisecond
	if waitForDrop(context.Background(), cfg, p, func(time.Duration) {}, log) {
		t.Fatal("expected no drop")
	}
	if !strings.Contains(buf.String(), "no drop observed") {
		t.Fatalf("missing log: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "still up") {
		t.Fatalf("missing still-up log: %q", buf.String())
	}
}

func TestWaitForDrop_NilPinger(t *testing.T) {
	var buf bytes.Buffer
	if waitForDrop(context.Background(), testCfg(), nil, func(time.Duration) {}, NewLogger(&buf)) {
		t.Fatal("nil pinger never drops")
	}
}

func TestWaitForDrop_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	if waitForDrop(ctx, testCfg(), &SequencePinger{Seq: []bool{true}}, func(time.Duration) {}, NewLogger(&buf)) {
		t.Fatal("cancelled should not drop")
	}
}

func TestWaitForUp_Immediate(t *testing.T) {
	var buf bytes.Buffer
	p := &SequencePinger{Seq: []bool{true}}
	el, err := waitForUp(context.Background(), testCfg(), p, func(time.Duration) {}, NewLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if el < 0 {
		t.Fatal("bad elapsed")
	}
}

func TestWaitForUp_AfterDown(t *testing.T) {
	var buf bytes.Buffer
	p := &SequencePinger{Seq: []bool{false, false, true}}
	cfg := testCfg()
	cfg.PingTimeout = 5 * time.Second
	cfg.PingInterval = time.Millisecond
	el, err := waitForUp(context.Background(), cfg, p, func(time.Duration) {}, NewLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	_ = el
	if p.Calls() != 3 {
		t.Fatalf("calls %d", p.Calls())
	}
}

func TestWaitForUp_Timeout(t *testing.T) {
	var buf bytes.Buffer
	p := &SequencePinger{Seq: []bool{false}}
	cfg := testCfg()
	cfg.PingTimeout = 20 * time.Millisecond
	cfg.PingInterval = time.Millisecond
	if _, err := waitForUp(context.Background(), cfg, p, func(time.Duration) {}, NewLogger(&buf)); err == nil {
		t.Fatal("expected timeout")
	} else if !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("wrong error %v", err)
	}
}

func TestWaitForUp_NilPinger(t *testing.T) {
	var buf bytes.Buffer
	if _, err := waitForUp(context.Background(), testCfg(), nil, func(time.Duration) {}, NewLogger(&buf)); err == nil {
		t.Fatal("expected error")
	}
}

func TestWaitForUp_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	if _, err := waitForUp(ctx, testCfg(), &SequencePinger{Seq: []bool{false}}, func(time.Duration) {}, NewLogger(&buf)); err == nil {
		t.Fatal("expected cancel error")
	}
}

func testCfg() Config {
	return Config{
		URL: "http://example.com", User: "root", Pass: "p",
		PingTimeout: 5 * time.Second, DropTimeout: 5 * time.Second,
		RebootMax: 5 * time.Second, Pace: 0,
		PingInterval: time.Millisecond, DownSleep: time.Millisecond,
		PingHost: "1.1.1.1",
	}
}
