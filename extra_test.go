package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- transport-error paths (mock server closed: no real hardware) ---

func closedTestClient(t *testing.T) *ONTClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // further dials fail fast (connection refused), no hardware
	c, err := NewONTClient(url, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Shorten dial timeouts via context so tests stay fast.
	return c
}

func TestONT_TransportErrorsAreFatal(t *testing.T) {
	c := closedTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.GetLoginPage(ctx); err == nil {
		t.Fatal("expected GetLoginPage transport error")
	}
	if _, err := c.FetchLoginToken(ctx); err == nil {
		t.Fatal("expected FetchLoginToken transport error")
	}
	if err := c.Login(ctx, "u", "p", strings.Repeat("a", 32)); err == nil {
		t.Fatal("expected Login transport error")
	}
	if _, err := c.FetchOntToken(ctx); err == nil {
		t.Fatal("expected FetchOntToken transport error")
	}
}

func TestONT_MaxBodyDefault(t *testing.T) {
	c := &ONTClient{MaxBody: 0}
	if c.maxBody() != 4<<20 {
		t.Fatalf("got %d", c.maxBody())
	}
	c.MaxBody = 123
	if c.maxBody() != 123 {
		t.Fatal("bad")
	}
}

func TestLoadDotEnvFile_IsDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadDotEnvFile(dir); err == nil {
		t.Fatal("expected error reading directory as file")
	}
}

// --- asNetError branches ---

type timeoutNetErr struct{ timeout bool }

func (e timeoutNetErr) Error() string   { return "net timeout" }
func (e timeoutNetErr) Timeout() bool   { return e.timeout }
func (e timeoutNetErr) Temporary() bool { return false }

type wrapErr struct{ err error }

func (w wrapErr) Error() string { return "wrap: " + w.err.Error() }
func (w wrapErr) Unwrap() error { return w.err }

type multiWrapErr struct{ errs []error }

func (m multiWrapErr) Error() string   { return "multi" }
func (m multiWrapErr) Unwrap() []error { return m.errs }

func TestAsNetError_Direct(t *testing.T) {
	var out net.Error
	if !asNetError(timeoutNetErr{true}, &out) || !out.Timeout() {
		t.Fatal("direct net.Error not found")
	}
}

func TestAsNetError_WrappedSingle(t *testing.T) {
	var out net.Error
	if !asNetError(wrapErr{timeoutNetErr{true}}, &out) {
		t.Fatal("wrapped not found")
	}
}

func TestAsNetError_WrappedMulti(t *testing.T) {
	var out net.Error
	m := multiWrapErr{[]error{errors.New("x"), wrapErr{timeoutNetErr{false}}}}
	if !asNetError(m, &out) {
		t.Fatal("multi wrapped not found")
	}
	m2 := multiWrapErr{[]error{errors.New("x")}}
	if asNetError(m2, &out) {
		t.Fatal("should not find net.Error")
	}
	if asNetError(errors.New("plain"), &out) {
		t.Fatal("plain should not match")
	}
	if asNetError(nil, &out) {
		t.Fatal("nil should not match")
	}
}

func TestClassifyRebootError_NetTimeout(t *testing.T) {
	ctx := context.Background()
	out, err := classifyRebootError(ctx, wrapErr{timeoutNetErr{true}})
	if err != nil || out != RebootTimeout {
		t.Fatalf("got %q %v", out, err)
	}
	// Non-timeout net.Error containing reset text still maps to reset.
	out, err = classifyRebootError(ctx, timeoutNetErr{false})
	// timeoutNetErr message is "net timeout" without reset/eof; not timeout-flagged
	// via Timeout()==false, so it falls to message checks -> contains "timeout"
	// but not "context", so it is fatal. That is correct: a non-timeout error
	// claiming timeout in text alone is not trusted.
	if err == nil {
		t.Fatalf("expected fatal, got %q", out)
	}
}

func TestClassifyRebootError_BrokenPipe(t *testing.T) {
	ctx := context.Background()
	if out, err := classifyRebootError(ctx, errors.New("write: broken pipe")); err != nil || out != RebootReset {
		t.Fatalf("got %q %v", out, err)
	}
}

// --- publicip constructor + failure-tolerant lookups (no external DNS) ---

func TestNewPublicIPResolver_Defaults(t *testing.T) {
	r := NewPublicIPResolver()
	if r == nil || r.LookupA == nil || r.LookupTXT == nil || r.Timeout <= 0 {
		t.Fatalf("%+v", r)
	}
}

func TestLookupVia_InvalidServerFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// 127.0.0.1:1 is closed; no external traffic, fails immediately.
	if _, err := LookupHostVia(ctx, "example.com", "127.0.0.1:1"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := LookupTXTComprisedVia(ctx, "example.com", "127.0.0.1:1"); err == nil {
		t.Fatal("expected error")
	}
}

// --- notifier defaults (no network, safe) ---

func TestNewNotifier_DefaultsSafe(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf, nil, nil)
	if n == nil {
		t.Fatal("nil")
	}
	// Must not panic; either notifies or falls back.
	_ = n("reconnected", "test body")
}

// --- extract edge branches ---

func TestExtractOntToken_EdgeBranches(t *testing.T) {
	// Tag with onttoken but no value attr.
	if _, err := ExtractOntToken(`<input name="onttoken" id="x">`); err == nil {
		t.Fatal("expected error")
	}
	// onttoken text outside any tag.
	if _, err := ExtractOntToken(`onttoken but no angle brackets`); err == nil {
		t.Fatal("expected error")
	}
	// Unclosed tag.
	if _, err := ExtractOntToken(`<input name="onttoken" value="abc`); err == nil {
		t.Fatal("expected error")
	}
	// attrValue with missing closing quote.
	if _, ok := attrValue(`<input value="abc>`, "value"); ok {
		t.Fatal("should not parse")
	}
	// extractValueFirst with onttoken outside tag then valid tag.
	html := `onttoken <input value="` + strings.Repeat("a", 32) + `" name="onttoken">`
	tok, err := ExtractOntToken(html)
	if err != nil || tok != strings.Repeat("a", 32) {
		t.Fatalf("got %q %v", tok, err)
	}
}

// --- waitForUp periodic logging branch (fast via small interval) ---

func TestWaitForUpEvery_LogsPeriodically(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf)
	cfg := testCfg()
	cfg.PingTimeout = 60 * time.Millisecond
	cfg.PingInterval = time.Millisecond
	p := &SequencePinger{Seq: []bool{false}} // always down -> timeout
	start := time.Now()
	_, err := waitForUpEvery(context.Background(), cfg, p, func(time.Duration) {}, log, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("too slow")
	}
	if !strings.Contains(buf.String(), "still down after") {
		t.Fatalf("missing periodic log:\n%s", buf.String())
	}
}

func TestWaitForUpEvery_DefaultInterval(t *testing.T) {
	var buf bytes.Buffer
	cfg := testCfg()
	cfg.PingTimeout = 10 * time.Millisecond
	cfg.PingInterval = time.Millisecond
	if _, err := waitForUpEvery(context.Background(), cfg, &SequencePinger{Seq: []bool{true}}, func(time.Duration) {}, NewLogger(&buf), 0); err != nil {
		t.Fatal(err)
	}
}

// --- Reboot with zero RebootMax falls back to default ---

func TestReboot_ZeroMaxUsesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c, err := NewONTClient(srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.RebootMax = 0
	if out, err := c.Reboot(context.Background(), strings.Repeat("a", 32)); err != nil || out != RebootClean {
		t.Fatalf("got %q %v", out, err)
	}
}

// --- ParseDotEnv comment/edge lines ---

func TestParseDotEnv_EdgeLines(t *testing.T) {
	got := ParseDotEnv("export \n# only comment\n   \nVALID=1\nBAD-KEY=2\n")
	if got["VALID"] != "1" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["BAD-KEY"]; ok {
		t.Fatalf("bad key kept %v", got)
	}
}
