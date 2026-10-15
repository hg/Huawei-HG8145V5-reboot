package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseDotEnv_Basic(t *testing.T) {
	in := `
# comment
ONT_URL=http://192.168.100.1
ONT_USER=root
ONT_PASS=secret123

export FOO=bar
EMPTY=
NOEQ
`
	got := ParseDotEnv(in)
	want := map[string]string{
		"ONT_URL":  "http://192.168.100.1",
		"ONT_USER": "root",
		"ONT_PASS": "secret123",
		"FOO":      "bar",
		"EMPTY":    "",
		"NOEQ":     "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseDotEnv_Quoting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"single literal", "A='hello # not comment'", map[string]string{"A": "hello # not comment"}},
		{"double escapes", `A="a\nb\rc\td\"e\\f\$g"`, map[string]string{"A": "a\nb\rc\td\"e\\f$g"}},
		{"unquoted trims", "A=  hello  ", map[string]string{"A": "hello"}},
		{"inline comment", "A=hello # comment here", map[string]string{"A": "hello"}},
		{"inline comment tab", "A=hello\t# comment", map[string]string{"A": "hello"}},
		{"hash no space kept", "A=hello#nospace", map[string]string{"A": "hello#nospace"}},
		{"export prefix", "export ONT_PASS= xyz ", map[string]string{"ONT_PASS": "xyz"}},
		{"spaces around eq", "  ONT_USER  =  root  ", map[string]string{"ONT_USER": "root"}},
		{"invalid key skipped", "0BAD=oops\nGOOD=yes", map[string]string{"GOOD": "yes"}},
		{"empty key skipped", "=oops\nGOOD=yes", map[string]string{"GOOD": "yes"}},
		{"double unmatched stays", `A="unclosed`, map[string]string{"A": `"unclosed`}},
		{"single unmatched stays", `A='unclosed`, map[string]string{"A": `'unclosed`}},
		{"hash only value", "A=#comment", map[string]string{"A": "#comment"}},
		{"quoted hash", `A="a # b"`, map[string]string{"A": "a # b"}},
		{"backtick escape", "A=\"a\\`b\"", map[string]string{"A": "a`b"}},
		{"unknown escape kept", `A="a\qb"`, map[string]string{"A": `a\qb`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDotEnv(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestLoadDotEnvFile_Missing(t *testing.T) {
	m, err := LoadDotEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty, got %v", m)
	}
}

func TestLoadDotEnvFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("ONT_PASS=hunter2\nONT_USER=admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadDotEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["ONT_PASS"] != "hunter2" || m["ONT_USER"] != "admin" {
		t.Fatalf("bad map %v", m)
	}
}

func TestLoadConfig_DefaultsAndValidation(t *testing.T) {
	// No env files, ONT_PASS via overrides.
	cfg, err := LoadConfig(map[string]string{"ONT_PASS": "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != DefaultONTURL || cfg.User != DefaultONTUser {
		t.Fatalf("bad defaults %+v", cfg)
	}
	if cfg.PingTimeout != DefaultPingTimeout || cfg.DropTimeout != DefaultDropTimeout || cfg.RebootMax != DefaultRebootMax {
		t.Fatalf("bad timeout defaults %+v", cfg)
	}
	if cfg.Pace != DefaultPace || cfg.PingHost != DefaultPingHost {
		t.Fatalf("bad pace/host %+v", cfg)
	}
}

func TestLoadConfig_MissingPass(t *testing.T) {
	// Ensure env doesn't leak ONT_PASS into the test.
	t.Setenv("ONT_PASS", "")
	// Also need dotenv files empty.
	_, err := LoadConfig(nil, nil)
	if err == nil || !contains(err.Error(), "ONT_PASS") {
		t.Fatalf("expected ONT_PASS error, got %v", err)
	}
}

func TestLoadConfig_Precedence(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "ONT_URL=http://from-file\nONT_USER=fileuser\nONT_PASS=filepass\nPING_TIMEOUT=111\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONT_USER", "envuser")
	t.Setenv("ONT_PASS", "envpass")
	// No overrides: env wins over file.
	cfg, err := LoadConfig(nil, []string{envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "http://from-file" {
		t.Fatalf("file URL not picked: %q", cfg.URL)
	}
	if cfg.User != "envuser" {
		t.Fatalf("env should win over file: %q", cfg.User)
	}
	if cfg.Pass != "envpass" {
		t.Fatalf("env pass should win: %q", cfg.Pass)
	}
	if cfg.PingTimeout != 111*time.Second {
		t.Fatalf("bad ping timeout %v", cfg.PingTimeout)
	}
	// Overrides win over env.
	cfg2, err := LoadConfig(map[string]string{"ONT_USER": "flaguser"}, []string{envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.User != "flaguser" {
		t.Fatalf("override should win: %q", cfg2.User)
	}
	// Empty override does not clobber.
	cfg3, err := LoadConfig(map[string]string{"ONT_USER": ""}, []string{envFile})
	if err != nil {
		t.Fatal(err)
	}
	if cfg3.User != "envuser" {
		t.Fatalf("empty override clobbered: %q", cfg3.User)
	}
}

func TestLoadConfig_DurationForms(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	if err := os.WriteFile(f, []byte("ONT_PASS=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PING_TIMEOUT", "2m")
	t.Setenv("DROP_TIMEOUT", "45")
	t.Setenv("REBOOT_MAX_TIME", "15s")
	t.Setenv("ONT_PACE", "0")
	t.Setenv("ONT_PING_INTERVAL", "1s")
	t.Setenv("ONT_DOWN_SLEEP", "3")
	t.Setenv("ONT_PING_HOST", "9.9.9.9")
	cfg, err := LoadConfig(nil, []string{f})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PingTimeout != 2*time.Minute {
		t.Fatalf("ping %v", cfg.PingTimeout)
	}
	if cfg.DropTimeout != 45*time.Second {
		t.Fatalf("drop %v", cfg.DropTimeout)
	}
	if cfg.RebootMax != 15*time.Second {
		t.Fatalf("reboot %v", cfg.RebootMax)
	}
	if cfg.Pace != 0 {
		t.Fatalf("pace %v", cfg.Pace)
	}
	if cfg.PingHost != "9.9.9.9" {
		t.Fatalf("host %q", cfg.PingHost)
	}
}

func TestLoadConfig_BadDurations(t *testing.T) {
	for _, kv := range []string{"PING_TIMEOUT", "DROP_TIMEOUT", "REBOOT_MAX_TIME", "ONT_PACE", "ONT_PING_INTERVAL", "ONT_DOWN_SLEEP"} {
		t.Run(kv, func(t *testing.T) {
			t.Setenv(kv, "not-a-duration!!")
			t.Setenv("ONT_PASS", "x")
			if _, err := LoadConfig(nil, nil); err == nil {
				t.Fatalf("expected error for %s", kv)
			}
		})
	}
}

func TestLoadConfig_ValidateErrors(t *testing.T) {
	base := func() Config {
		return Config{
			URL: "http://x", User: "u", Pass: "p",
			PingTimeout: time.Second, DropTimeout: 0, RebootMax: time.Second,
			Pace: 0, PingInterval: time.Second, PingHost: "1.1.1.1", DownSleep: time.Second,
		}
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty url", func(c *Config) { c.URL = " " }},
		{"empty user", func(c *Config) { c.User = "" }},
		{"empty pass", func(c *Config) { c.Pass = "" }},
		{"zero ping timeout", func(c *Config) { c.PingTimeout = 0 }},
		{"negative drop", func(c *Config) { c.DropTimeout = -time.Second }},
		{"zero reboot", func(c *Config) { c.RebootMax = 0 }},
		{"negative pace", func(c *Config) { c.Pace = -time.Second }},
		{"zero ping interval", func(c *Config) { c.PingInterval = 0 }},
		{"empty ping host", func(c *Config) { c.PingHost = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseSecondsOrDuration(t *testing.T) {
	d, err := parseSecondsOrDuration("600", "X")
	if err != nil || d != 600*time.Second {
		t.Fatalf("got %v %v", d, err)
	}
	d, err = parseSecondsOrDuration("1.5", "X")
	if err != nil || d != 1500*time.Millisecond {
		t.Fatalf("got %v %v", d, err)
	}
	d, err = parseSecondsOrDuration("2m", "X")
	if err != nil || d != 2*time.Minute {
		t.Fatalf("got %v %v", d, err)
	}
	if _, err := parseSecondsOrDuration("", "X"); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := parseSecondsOrDuration("bogus!!", "X"); err == nil {
		t.Fatal("expected bad error")
	}
}

func TestDefaultEnvFiles(t *testing.T) {
	files := DefaultEnvFiles()
	if len(files) == 0 || files[0] != ".env" {
		t.Fatalf("bad %v", files)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
