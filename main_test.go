package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealMain_MissingPassErrorsWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, ".env")
	if err := os.WriteFile(empty, []byte("# empty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONT_PASS", "")
	t.Setenv("ONT_URL", "")
	t.Setenv("ONT_USER", "")
	// Point at an empty env file and no pass flag: must fail on ONT_PASS
	// before any network is touched.
	err := realMain([]string{"--env-file", empty})
	if err == nil || !strings.Contains(err.Error(), "ONT_PASS") {
		t.Fatalf("got %v", err)
	}
}

func TestRealMain_BadFlag(t *testing.T) {
	if err := realMain([]string{"--bogus-flag"}); err == nil {
		t.Fatal("expected flag error")
	}
}

func TestRealMain_BadTimeoutValue(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	if err := os.WriteFile(f, []byte("ONT_PASS=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONT_PASS", "")
	// --ping-timeout bogus must error during config load, no network.
	if err := realMain([]string{"--env-file", f, "--ping-timeout", "bogus!!"}); err == nil {
		t.Fatal("expected error")
	}
}
