package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestNewNotifier_FallbackWhenMissing(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf,
		func(string) (string, error) { return "", errors.New("not found") },
		func(string, ...string) error { t.Fatal("should not run"); return nil },
	)
	if err := n("reconnected", "body"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "reconnected" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestNewNotifier_FallbackWhenRunFails(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf,
		func(string) (string, error) { return "/usr/bin/notify-send", nil },
		func(string, ...string) error { return errors.New("failed") },
	)
	if err := n("s", "b"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "reconnected" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestNewNotifier_SuccessNoFallback(t *testing.T) {
	var buf bytes.Buffer
	var gotArgs []string
	n := NewNotifier(&buf,
		func(string) (string, error) { return "/usr/bin/notify-send", nil },
		func(name string, args ...string) error {
			gotArgs = append([]string{name}, args...)
			return nil
		},
	)
	if err := n("reconnected", "hello"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("should not print fallback, got %q", buf.String())
	}
	if len(gotArgs) != 3 || gotArgs[0] != "notify-send" || gotArgs[1] != "reconnected" {
		t.Fatalf("args %v", gotArgs)
	}
}
