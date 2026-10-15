package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLogger_Format(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	l.Log("hello")
	out := buf.String()
	if !strings.HasPrefix(out, "[") || !strings.Contains(out, "] hello\n") {
		t.Fatalf("bad log line %q", out)
	}
	// HH:MM:SS shape: [8] + time + "] "
	if len(out) < 10 || out[0] != '[' || out[9] != ']' {
		t.Fatalf("bad timestamp %q", out)
	}
}

func TestLogger_Logf(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	l.Logf("n=%d s=%s", 42, "x")
	if !strings.Contains(buf.String(), "n=42 s=x") {
		t.Fatalf("bad %q", buf.String())
	}
}

func TestLogger_NilSafe(t *testing.T) {
	var l *Logger
	l.Log("no panic")
	l.Logf("no panic %d", 1)
}

func TestFormatSecs(t *testing.T) {
	if formatSecs(20*time.Second) != "20s" {
		t.Fatal("bad")
	}
	if formatSecs(1500*time.Millisecond) != "2s" { // rounds
		t.Fatalf("got %q", formatSecs(1500*time.Millisecond))
	}
	if formatSecs(0) != "0s" {
		t.Fatal("bad zero")
	}
}
