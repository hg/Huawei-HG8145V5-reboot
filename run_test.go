package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeONT is a scriptable ONT double. No network is touched.
type fakeONT struct {
	loginPageErr error
	loginToken   string
	loginTokErr  error
	loginErr     error
	ontToken     string
	ontTokErr    error
	rebootOut    RebootOutcome
	rebootErr    error

	calls       []string
	gotUser     string
	gotPass     string
	gotLoginTok string
	gotRebootTk string
}

func (f *fakeONT) GetLoginPage(ctx context.Context) error {
	f.calls = append(f.calls, "GetLoginPage")
	return f.loginPageErr
}
func (f *fakeONT) FetchLoginToken(ctx context.Context) (string, error) {
	f.calls = append(f.calls, "FetchLoginToken")
	return f.loginToken, f.loginTokErr
}
func (f *fakeONT) Login(ctx context.Context, user, pass, token string) error {
	f.calls = append(f.calls, "Login")
	f.gotUser, f.gotPass, f.gotLoginTok = user, pass, token
	return f.loginErr
}
func (f *fakeONT) FetchOntToken(ctx context.Context) (string, error) {
	f.calls = append(f.calls, "FetchOntToken")
	return f.ontToken, f.ontTokErr
}
func (f *fakeONT) Reboot(ctx context.Context, tok string) (RebootOutcome, error) {
	f.calls = append(f.calls, "Reboot")
	f.gotRebootTk = tok
	return f.rebootOut, f.rebootErr
}

func goodTokens() (string, string) {
	return strings.Repeat("a", 32), strings.Repeat("b", 64)
}

func runTestCfg() Config {
	return Config{
		URL: "http://mock", User: "root", Pass: "s3cret",
		PingTimeout: 5 * time.Second, DropTimeout: 50 * time.Millisecond,
		RebootMax: 5 * time.Second, Pace: 0,
		PingInterval: time.Millisecond, DownSleep: time.Millisecond,
		PingHost: "1.1.1.1",
	}
}

func TestRun_SuccessDropObservedIPChanged(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	var buf bytes.Buffer
	log := NewLogger(&buf)
	ips := []string{"1.1.1.1", "2.2.2.2"}
	n := 0
	pub := func(ctx context.Context) string {
		v := ips[n]
		if n < len(ips)-1 {
			n++
		}
		return v
	}
	// drop: up,down,down(final) then up for recovery.
	p := &SequencePinger{Seq: []bool{true, false, false, true}}
	var notified []string
	deps := RunDeps{
		ONT: f, PublicIP: pub, Pinger: p,
		Notify: func(s, b string) error { notified = append(notified, s+"|"+b); return nil },
		Sleep:  func(time.Duration) {}, Log: log,
	}
	cfg := runTestCfg()
	res, err := Run(context.Background(), cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dropped {
		t.Fatal("expected dropped")
	}
	if res.BeforeIP != "1.1.1.1" || res.AfterIP != "2.2.2.2" {
		t.Fatalf("%+v", res)
	}
	if res.Reboot != RebootClean {
		t.Fatalf("reboot %q", res.Reboot)
	}
	out := buf.String()
	for _, want := range []string{
		"public IP before reset: 1.1.1.1",
		"GET login page",
		"fetch login token",
		"login as root",
		"login OK",
		"fetch reboot token",
		"onttoken=" + ot[:12],
		"reboot posted (clean response)",
		"network down, sleeping",
		"ping 1.1.1.1 OK after",
		"drop was observed",
		"public IP after reset: 2.2.2.2",
		"IP changed: 1.1.1.1 -> 2.2.2.2",
		"notification sent",
		"done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(notified) != 1 || !strings.HasPrefix(notified[0], "reconnected|") {
		t.Fatalf("notify %v", notified)
	}
	if !strings.Contains(notified[0], "1.1.1.1 -> 2.2.2.2") {
		t.Fatalf("notify body %q", notified[0])
	}
	// Order check.
	wantOrder := []string{"GetLoginPage", "FetchLoginToken", "Login", "FetchOntToken", "Reboot"}
	if fmt.Sprint(f.calls) != fmt.Sprint(wantOrder) {
		t.Fatalf("calls %v", f.calls)
	}
	if f.gotUser != "root" || f.gotPass != "s3cret" || f.gotLoginTok != lt || f.gotRebootTk != ot {
		t.Fatalf("args %+v", f)
	}
}

func TestRun_SuccessNoDropIPUnchanged(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootReset}
	var buf bytes.Buffer
	deps := RunDeps{
		ONT:      f,
		PublicIP: func(ctx context.Context) string { return "9.9.9.9" },
		Pinger:   &SequencePinger{Seq: []bool{true}}, // always up
		Notify:   func(s, b string) error { return nil },
		Sleep:    func(time.Duration) {},
		Log:      NewLogger(&buf),
	}
	cfg := runTestCfg()
	cfg.DropTimeout = 10 * time.Millisecond
	res, err := Run(context.Background(), cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dropped {
		t.Fatal("should not be dropped")
	}
	out := buf.String()
	if !strings.Contains(out, "no drop observed") {
		t.Fatalf("missing no-drop log:\n%s", out)
	}
	if !strings.Contains(out, "network still up, skipping down-sleep") {
		t.Fatalf("missing still-up:\n%s", out)
	}
	if !strings.Contains(out, "no drop observed") || !strings.Contains(out, "outage was missed") {
		t.Fatalf("bad:\n%s", out)
	}
	if !strings.Contains(out, "IP unchanged: 9.9.9.9") {
		t.Fatalf("missing unchanged:\n%s", out)
	}
	if !strings.Contains(out, "connection reset by peer") {
		t.Fatalf("missing reset log:\n%s", out)
	}
}

func TestRun_RebootTimeoutLog(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootTimeout}
	var buf bytes.Buffer
	deps := RunDeps{
		ONT: f, PublicIP: func(ctx context.Context) string { return unknownIP },
		Pinger: &SequencePinger{Seq: []bool{true}}, Notify: nil,
		Sleep: func(time.Duration) {}, Log: NewLogger(&buf),
	}
	cfg := runTestCfg()
	cfg.DropTimeout = 10 * time.Millisecond
	if _, err := Run(context.Background(), cfg, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "POST timed out after 5s") {
		t.Fatalf("missing timeout log:\n%s", buf.String())
	}
}

func TestRun_ErrorsPropagate(t *testing.T) {
	lt, ot := goodTokens()
	cases := []struct {
		name    string
		mutate  func(*fakeONT)
		wantErr string
		calls   int // expected calls before failure
	}{
		{"login page", func(f *fakeONT) { f.loginPageErr = errors.New("nope") }, "nope", 1},
		{"login token", func(f *fakeONT) { f.loginTokErr = errors.New("bad token") }, "bad token", 2},
		{"login", func(f *fakeONT) { f.loginErr = errors.New("login failed: x") }, "login failed", 3},
		{"onttoken", func(f *fakeONT) { f.ontTokErr = errors.New("onttoken not found") }, "onttoken", 4},
		{"reboot fatal", func(f *fakeONT) { f.rebootErr = errors.New("reboot POST failed") }, "reboot POST failed", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
			tc.mutate(f)
			var buf bytes.Buffer
			deps := RunDeps{
				ONT: f, PublicIP: func(ctx context.Context) string { return "1.1.1.1" },
				Pinger: &SequencePinger{Seq: []bool{true}}, Notify: nil,
				Sleep: func(time.Duration) {}, Log: NewLogger(&buf),
			}
			if _, err := Run(context.Background(), runTestCfg(), deps); err == nil {
				t.Fatal("expected error")
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("wrong error %v", err)
			}
			if len(f.calls) != tc.calls {
				t.Fatalf("calls %v", f.calls)
			}
		})
	}
}

func TestRun_PingTimeoutFatal(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	var buf bytes.Buffer
	deps := RunDeps{
		ONT: f, PublicIP: func(ctx context.Context) string { return "1.1.1.1" },
		Pinger: &SequencePinger{Seq: []bool{false}}, // never recovers
		Notify: nil, Sleep: func(time.Duration) {}, Log: NewLogger(&buf),
	}
	cfg := runTestCfg()
	cfg.PingTimeout = 15 * time.Millisecond
	cfg.DropTimeout = 5 * time.Millisecond
	if _, err := Run(context.Background(), cfg, deps); err == nil {
		t.Fatal("expected ping timeout")
	} else if !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("wrong %v", err)
	}
}

func TestRun_PaceUsesSleeper(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	var buf bytes.Buffer
	var slept []time.Duration
	deps := RunDeps{
		ONT: f, PublicIP: func(ctx context.Context) string { return "1.1.1.1" },
		Pinger: &SequencePinger{Seq: []bool{true}}, Notify: nil,
		Sleep: func(d time.Duration) { slept = append(slept, d) },
		Log:   NewLogger(&buf),
	}
	cfg := runTestCfg()
	cfg.Pace = 7 * time.Millisecond
	cfg.DropTimeout = 5 * time.Millisecond
	if _, err := Run(context.Background(), cfg, deps); err != nil {
		t.Fatal(err)
	}
	// 4 pace sleeps expected (after login page, token, login, onttoken).
	paceCount := 0
	for _, d := range slept {
		if d == 7*time.Millisecond {
			paceCount++
		}
	}
	if paceCount != 4 {
		t.Fatalf("pace sleeps %v", slept)
	}
}

func TestRun_NilDepsDefaults(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	cfg := runTestCfg()
	cfg.DropTimeout = 5 * time.Millisecond
	// No PublicIP (unknown), no Pinger (no drop, but waitForUp errors), nil Log/Sleep.
	// waitForUp with nil pinger must error.
	_, err := Run(context.Background(), cfg, RunDeps{ONT: f})
	if err == nil {
		t.Fatal("expected error with nil pinger")
	}
}

func TestRun_CancelledContext(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	_, err := Run(ctx, runTestCfg(), RunDeps{
		ONT: f, PublicIP: func(ctx context.Context) string { return "x" },
		Pinger: &SequencePinger{Seq: []bool{true}},
		Sleep:  func(time.Duration) {}, Log: NewLogger(&buf),
	})
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if len(f.calls) != 0 {
		t.Fatalf("should not call ONT when cancelled: %v", f.calls)
	}
}

func TestRun_NilNotifySkips(t *testing.T) {
	lt, ot := goodTokens()
	f := &fakeONT{loginToken: lt, ontToken: ot, rebootOut: RebootClean}
	var buf bytes.Buffer
	deps := RunDeps{
		ONT: f, PublicIP: func(ctx context.Context) string { return "1.1.1.1" },
		Pinger: &SequencePinger{Seq: []bool{true}}, Notify: nil,
		Sleep: func(time.Duration) {}, Log: NewLogger(&buf),
	}
	cfg := runTestCfg()
	cfg.DropTimeout = 5 * time.Millisecond
	if _, err := Run(context.Background(), cfg, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "notification sent") {
		t.Fatal("should not log notification sent with nil notifier")
	}
	if !strings.Contains(buf.String(), "done") {
		t.Fatal("missing done")
	}
}

// TestRun_IntegrationWithMockServer exercises the real ONTClient against a
// mock HTTP server implementing the exact shell flow, with stub DNS/ping.
// This is the closest to hardware without touching it.
func TestRun_IntegrationWithMockServer(t *testing.T) {
	loginTok := strings.Repeat("a", 32)
	ontTok := strings.Repeat("b", 64)
	var seenLogin, seenReboot map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/":
			w.WriteHeader(200)
			_, _ = w.Write([]byte("<html>login</html>"))
		case r.Method == "POST" && r.URL.Path == "/asp/GetRandCount.asp":
			w.WriteHeader(200)
			_, _ = w.Write([]byte("\uFEFF" + loginTok + "\r\n"))
		case r.Method == "POST" && r.URL.Path == "/login.cgi":
			_ = r.ParseForm()
			seenLogin = map[string]string{
				"UserName":     r.FormValue("UserName"),
				"PassWord":     r.FormValue("PassWord"),
				"Language":     r.FormValue("Language"),
				"x.X_HW_Token": r.FormValue("x.X_HW_Token"),
			}
			if r.FormValue("x.X_HW_Token") != loginTok {
				_, _ = w.Write([]byte("pageName = '/'"))
				return
			}
			_, _ = w.Write([]byte("top.location.replace('index.asp'); pageName = 'index.asp'"))
		case r.Method == "GET" && r.URL.Path == "/html/ssmp/accoutcfg/ontmngt.asp":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<html><input type="hidden" name="onttoken" value="` + ontTok + `"></html>`))
		case r.Method == "POST" && r.URL.Path == "/html/ssmp/accoutcfg/set.cgi":
			_ = r.ParseForm()
			seenReboot = map[string]string{
				"x":           r.URL.Query().Get("x"),
				"RequestFile": r.URL.Query().Get("RequestFile"),
				"token":       r.FormValue("x.X_HW_Token"),
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	client, err := NewONTClient(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := runTestCfg()
	cfg.URL = srv.URL
	cfg.User = "root"
	cfg.Pass = "pw123"
	deps := RunDeps{
		ONT: client,
		PublicIP: func() func(context.Context) string {
			n := 0
			ips := []string{"10.0.0.1", "10.0.0.2"}
			return func(context.Context) string {
				v := ips[n]
				if n < len(ips)-1 {
					n++
				}
				return v
			}
		}(),
		Pinger: &SequencePinger{Seq: []bool{true, false, false, true}},
		Notify: func(s, b string) error { return nil },
		Sleep:  func(time.Duration) {},
		Log:    NewLogger(&buf),
	}
	res, err := Run(context.Background(), cfg, deps)
	if err != nil {
		t.Fatalf("run failed: %v\nlogs:\n%s", err, buf.String())
	}
	if res.BeforeIP != "10.0.0.1" || res.AfterIP != "10.0.0.2" {
		t.Fatalf("%+v", res)
	}
	if seenLogin["UserName"] != "root" || seenLogin["PassWord"] != EncodePassword("pw123") ||
		seenLogin["Language"] != "english" || seenLogin["x.X_HW_Token"] != loginTok {
		t.Fatalf("login form %v", seenLogin)
	}
	if seenReboot["x"] != "InternetGatewayDevice.X_HW_DEBUG.SMP.DM.ResetBoard" ||
		seenReboot["RequestFile"] != "html/ssmp/accoutcfg/ontmngt.asp" ||
		seenReboot["token"] != ontTok {
		t.Fatalf("reboot %v", seenReboot)
	}
}

// TestRun_IntegrationLoginFailure ensures a bad password surfaces as login failed.
func TestRun_IntegrationLoginFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.WriteHeader(200)
		case r.URL.Path == "/asp/GetRandCount.asp":
			_, _ = w.Write([]byte(strings.Repeat("a", 32)))
		case r.URL.Path == "/login.cgi":
			_, _ = w.Write([]byte("pageName = '/'"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	client, _ := NewONTClient(srv.URL, time.Second)
	var buf bytes.Buffer
	cfg := runTestCfg()
	cfg.URL = srv.URL
	_, err := Run(context.Background(), cfg, RunDeps{
		ONT: client, PublicIP: func(ctx context.Context) string { return "x" },
		Pinger: &SequencePinger{Seq: []bool{true}},
		Sleep:  func(time.Duration) {}, Log: NewLogger(&buf),
	})
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("got %v", err)
	}
}
