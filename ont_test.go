package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient spins an ONTClient against a mock HTTP server. No real
// hardware is ever contacted: all tests use httptest URLs.
func newTestClient(t *testing.T, srv *httptest.Server, rebootMax time.Duration) *ONTClient {
	t.Helper()
	if rebootMax <= 0 {
		rebootMax = 5 * time.Second
	}
	c, err := NewONTClient(srv.URL, rebootMax)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewONTClient_Errors(t *testing.T) {
	if _, err := NewONTClient("", time.Second); err == nil {
		t.Fatal("expected empty URL error")
	}
	if _, err := NewONTClient("://bad-url", time.Second); err == nil {
		t.Fatal("expected bad URL error")
	}
}

func TestNewONTClient_TrimsSlash(t *testing.T) {
	c, err := NewONTClient("http://example.com///", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "http://example.com" {
		t.Fatalf("got %q", c.BaseURL)
	}
}

func TestIsHexToken(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{strings.Repeat("a", 32), true},
		{strings.Repeat("0", 64), true},
		{strings.Repeat("f", 33), true},
		{"", false},
		{strings.Repeat("a", 31), false},
		{strings.Repeat("A", 32), false}, // shell regex is lowercase-only
		{strings.Repeat("g", 32), false},
		{strings.Repeat("a", 31) + " ", false},
		{"ab cd" + strings.Repeat("e", 27), false},
	}
	for _, tc := range cases {
		if got := IsHexToken(tc.in); got != tc.want {
			t.Errorf("IsHexToken(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestCleanRandCount(t *testing.T) {
	bom := "\uFEFF"
	cases := []struct {
		in   string
		want string
	}{
		{"abc123\r\n", "abc123"},
		{bom + strings.Repeat("b", 32) + "\r\n", strings.Repeat("b", 32)},
		{strings.Repeat("c", 32) + "\n", strings.Repeat("c", 32)},
		{"  " + strings.Repeat("d", 32) + "  ", strings.Repeat("d", 32)},
		{bom + bom + strings.Repeat("e", 32), strings.Repeat("e", 32)},
		{"a\rb\nc", "abc"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := CleanRandCount(tc.in); got != tc.want {
			t.Errorf("CleanRandCount(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodePassword(t *testing.T) {
	if got := EncodePassword("hello"); got != "aGVsbG8=" {
		t.Fatalf("got %q", got)
	}
	if got := EncodePassword(""); got != "" {
		t.Fatalf("got %q", got)
	}
	// Must match standard base64 with padding.
	if got := EncodePassword("p@ss:word/123"); got != "cEBzczp3b3JkLzEyMw==" {
		t.Fatalf("got %q", got)
	}
}

func TestGetLoginPage(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(200)
			_, _ = w.Write([]byte("login"))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if err := c.GetLoginPage(context.Background()); err != nil {
			t.Fatal(err)
		}
		if gotPath != "/" {
			t.Fatalf("path %q", gotPath)
		}
	})
	t.Run("http error like curl -f", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if err := c.GetLoginPage(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if err := c.GetLoginPage(context.Background()); err == nil {
			t.Fatal("expected 404 error")
		}
	})
}

func TestFetchLoginToken(t *testing.T) {
	good := strings.Repeat("a", 32)
	t.Run("ok with BOM and CRLF", func(t *testing.T) {
		var method, path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, path = r.Method, r.URL.Path
			w.WriteHeader(200)
			_, _ = w.Write([]byte("\uFEFF" + good + "\r\n"))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		tok, err := c.FetchLoginToken(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if tok != good {
			t.Fatalf("got %q", tok)
		}
		if method != "POST" || path != "/asp/GetRandCount.asp" {
			t.Fatalf("method %s path %s", method, path)
		}
	})
	t.Run("bare token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(good))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		tok, err := c.FetchLoginToken(context.Background())
		if err != nil || tok != good {
			t.Fatalf("got %q %v", tok, err)
		}
	})
	t.Run("bad token rejected", func(t *testing.T) {
		for _, body := range []string{"", "xyz", strings.Repeat("A", 32), "short", "<html>login</html>"} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			c := newTestClient(t, srv, 0)
			if _, err := c.FetchLoginToken(context.Background()); err == nil {
				srv.Close()
				t.Fatalf("expected error for %q", body)
			}
			srv.Close()
		}
	})
	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if _, err := c.FetchLoginToken(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestLogin(t *testing.T) {
	token := strings.Repeat("b", 32)
	t.Run("success checks form fields", func(t *testing.T) {
		var gotCT, gotUser, gotPass, gotLang, gotTok string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/login.cgi" || r.Method != "POST" {
				w.WriteHeader(404)
				return
			}
			gotCT = r.Header.Get("Content-Type")
			_ = r.ParseForm()
			gotUser = r.FormValue("UserName")
			gotPass = r.FormValue("PassWord")
			gotLang = r.FormValue("Language")
			gotTok = r.FormValue("x.X_HW_Token")
			_, _ = w.Write([]byte("<script>pageName = 'index.asp'</script>"))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if err := c.Login(context.Background(), "root", "s3cret", token); err != nil {
			t.Fatal(err)
		}
		if gotCT != "application/x-www-form-urlencoded" {
			t.Fatalf("CT %q", gotCT)
		}
		if gotUser != "root" || gotLang != "english" || gotTok != token {
			t.Fatalf("form %q %q %q", gotUser, gotLang, gotTok)
		}
		if gotPass != EncodePassword("s3cret") {
			t.Fatalf("pass %q", gotPass)
		}
	})
	t.Run("failure page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("pageName = '/'"))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		err := c.Login(context.Background(), "root", "bad", token)
		if err == nil || !strings.Contains(err.Error(), "login failed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("failure snippet truncated to 200", func(t *testing.T) {
		long := strings.Repeat("Z", 500)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(long))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		err := c.Login(context.Background(), "root", "bad", token)
		if err == nil {
			t.Fatal("expected error")
		}
		if len(err.Error()) > 300 {
			t.Fatalf("not truncated: len %d", len(err.Error()))
		}
	})
	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if err := c.Login(context.Background(), "u", "p", token); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestExtractOntToken(t *testing.T) {
	good := strings.Repeat("c", 64)
	cases := []struct {
		name    string
		html    string
		want    string
		wantErr bool
	}{
		{"double quotes name first", `<input type="hidden" name="onttoken" value="` + good + `">`, good, false},
		{"extra attrs", `<input id="x" name="onttoken" class="y" value="` + good + `" />`, good, false},
		{"single quotes", `<input name='onttoken' value='` + good + `'>`, good, false},
		{"value before name", `<input value="` + good + `" name="onttoken">`, good, false},
		{"32 chars ok", `<input name="onttoken" value="` + strings.Repeat("d", 32) + `">`, strings.Repeat("d", 32), false},
		{"missing", `<html>no token</html>`, "", true},
		{"empty value", `<input name="onttoken" value="">`, "", true},
		{"uppercase rejected", `<input name="onttoken" value="` + strings.Repeat("A", 32) + `">`, "", true},
		{"short rejected", `<input name="onttoken" value="abc">`, "", true},
		{"multiple picks first", `<input name="onttoken" value="` + strings.Repeat("1", 32) + `"><input name="onttoken" value="` + strings.Repeat("2", 32) + `">`, strings.Repeat("1", 32), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractOntToken(tc.html)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !strings.Contains(err.Error(), "onttoken") {
					t.Fatalf("wrong error %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFetchOntToken_HTTP(t *testing.T) {
	good := strings.Repeat("e", 32)
	t.Run("ok", func(t *testing.T) {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_, _ = w.Write([]byte(`<input name="onttoken" value="` + good + `">`))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		tok, err := c.FetchOntToken(context.Background())
		if err != nil || tok != good {
			t.Fatalf("got %q %v", tok, err)
		}
		if path != "/html/ssmp/accoutcfg/ontmngt.asp" {
			t.Fatalf("path %q", path)
		}
	})
	t.Run("missing token errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html>pageName = '/'</html>`))
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if _, err := c.FetchOntToken(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer srv.Close()
		c := newTestClient(t, srv, 0)
		if _, err := c.FetchOntToken(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestReboot_Clean(t *testing.T) {
	ontToken := strings.Repeat("f", 32)
	var gotPath, gotQuery, gotTok, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotCT = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotTok = r.FormValue("x.X_HW_Token")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, 5*time.Second)
	out, err := c.Reboot(context.Background(), ontToken)
	if err != nil || out != RebootClean {
		t.Fatalf("got %q %v", out, err)
	}
	if gotPath != "/html/ssmp/accoutcfg/set.cgi" {
		t.Fatalf("path %q", gotPath)
	}
	if !strings.Contains(gotQuery, "ResetBoard") || !strings.Contains(gotQuery, "RequestFile=") {
		t.Fatalf("query %q", gotQuery)
	}
	if gotTok != ontToken {
		t.Fatalf("token %q", gotTok)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Fatalf("CT %q", gotCT)
	}
}

func TestReboot_HTTPErrorIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, 5*time.Second)
	if _, err := c.Reboot(context.Background(), strings.Repeat("a", 32)); err == nil {
		t.Fatal("expected fatal on 500")
	}
}

func TestReboot_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // longer than client cap
		_, _ = w.Write([]byte("too late"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, 200*time.Millisecond)
	start := time.Now()
	out, err := c.Reboot(context.Background(), strings.Repeat("a", 32))
	el := time.Since(start)
	if err != nil || out != RebootTimeout {
		t.Fatalf("got %q %v", out, err)
	}
	if el > 2*time.Second {
		t.Fatalf("did not cap at RebootMax: %v", el)
	}
}

func TestReboot_Reset(t *testing.T) {
	// Hijack and kill the connection mid-response to simulate the ONT dying.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijack")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // abrupt close -> client sees reset/EOF
	}))
	defer srv.Close()
	c := newTestClient(t, srv, 5*time.Second)
	out, err := c.Reboot(context.Background(), strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("reset should be success, got %v", err)
	}
	// Abrupt close surfaces as EOF/reset on the client; both map to RebootReset.
	// Some platforms surface it as clean? Accept reset only.
	if out != RebootReset {
		t.Fatalf("got %q want reset", out)
	}
}

func TestClassifyRebootError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	// Timeout via net.Error is tested through TestReboot_Timeout; here test strings.
	if out, err := classifyRebootError(ctx, fmt.Errorf("read: connection reset by peer")); err != nil || out != RebootReset {
		t.Fatalf("got %q %v", out, err)
	}
	if out, err := classifyRebootError(ctx, fmt.Errorf("unexpected EOF")); err != nil || out != RebootReset {
		t.Fatalf("got %q %v", out, err)
	}
	if _, err := classifyRebootError(ctx, fmt.Errorf("dial tcp: connection refused")); err == nil {
		t.Fatal("refused must be fatal")
	}
	if _, err := classifyRebootError(ctx, fmt.Errorf("no such host")); err == nil {
		t.Fatal("dns must be fatal")
	}
	// Cancelled context with deadline exceeded -> timeout.
	dctx, dcancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer dcancel()
	time.Sleep(5 * time.Millisecond)
	if out, err := classifyRebootError(dctx, fmt.Errorf("Client.Timeout exceeded")); err != nil || out != RebootTimeout {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("abc", 5) != "abc" {
		t.Fatal("bad")
	}
	if truncate("abcdef", 3) != "abc" {
		t.Fatal("bad")
	}
}
