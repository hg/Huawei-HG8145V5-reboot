package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Defaults matching reconnect.sh.
const (
	DefaultONTURL       = "http://192.168.100.1"
	DefaultONTUser      = "root"
	DefaultPingTimeout  = 600 * time.Second
	DefaultDropTimeout  = 90 * time.Second
	DefaultRebootMax    = 20 * time.Second
	DefaultPace         = 2 * time.Second
	DefaultPingInterval = 2 * time.Second
	DefaultDownSleep    = 5 * time.Second
	DefaultPingHost     = "1.1.1.1"
	DefaultStillDownEv  = 20 * time.Second
)

// Config holds all tunable behavior. Durations mirror the shell script:
// PING_TIMEOUT (default 600s), DROP_TIMEOUT (default 90s),
// REBOOT_MAX_TIME (default 20s), plus Pace (2s between ONT calls),
// PingInterval (2s between pings) and DownSleep (5s after a drop).
type Config struct {
	URL          string
	User         string
	Pass         string
	PingTimeout  time.Duration
	DropTimeout  time.Duration
	RebootMax    time.Duration
	Pace         time.Duration
	PingInterval time.Duration
	DownSleep    time.Duration
	PingHost     string
	// EnvFile, when non-empty, is loaded (if present) before reading the
	// process environment. Empty means auto-discovery (see DefaultEnvFiles).
	EnvFile string
}

// DefaultEnvFiles returns the candidate .env locations: ./\.env and the
// executable's directory .env (mirrors SCRIPT_DIR/.env in reconnect.sh).
func DefaultEnvFiles() []string {
	files := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" && dir != "." {
			files = append(files, filepath.Join(dir, ".env"))
		}
	}
	return files
}

// ParseDotEnv parses dotenv-style content into a map.
//
// Supported subset (enough for shell-sourced .env files):
//   - blank lines and lines starting with '#' are skipped
//   - optional leading "export " is allowed
//   - KEY=VALUE, KEY empty, or KEY without '=' (treated as empty)
//   - values may be unquoted, single-quoted (literal) or double-quoted
//     (with \\, \", \n, \r, \t, \$, \` escapes)
//   - unquoted values are trimmed and cut at an inline " #..." comment
//   - surrounding whitespace around key and value is ignored
func ParseDotEnv(data string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, hasEq := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !isEnvKey(key) {
			continue
		}
		if !hasEq {
			out[key] = ""
			continue
		}
		val = strings.TrimSpace(val)
		out[key] = parseDotEnvValue(val)
	}
	return out
}

func isEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}

func parseDotEnvValue(v string) string {
	if len(v) >= 2 {
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			return v[1 : len(v)-1]
		}
		if v[0] == '"' && v[len(v)-1] == '"' {
			inner := v[1 : len(v)-1]
			return unescapeDoubleQuoted(inner)
		}
	}
	// Unquoted: cut inline comments starting with space/hash or tab/hash.
	if idx := inlineCommentIndex(v); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	return strings.TrimSpace(v)
}

func unescapeDoubleQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\', '$', '`':
				b.WriteByte(s[i+1])
			default:
				b.WriteByte(s[i])
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// inlineCommentIndex finds the start of an inline " #..." comment that is
// outside of any quoting. v is already trimmed; quotes inside unquoted values
// are treated literally except for detecting the comment boundary.
func inlineCommentIndex(v string) int {
	inS, inD := false, false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\'' && !inD {
			inS = !inS
			continue
		}
		if c == '"' && !inS {
			inD = !inD
			continue
		}
		if inS || inD {
			continue
		}
		if c == '#' && i > 0 && (v[i-1] == ' ' || v[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// LoadDotEnvFile reads path and returns its parsed entries.
// Missing files return an empty map and no error.
func LoadDotEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return ParseDotEnv(string(data)), nil
}

// applyDotEnv copies entries into out only for keys not already present.
func applyDotEnv(out map[string]string, file map[string]string) {
	for k, v := range file {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
}

// envSnapshot returns a copy of the process environment as a map.
func envSnapshot() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// LoadConfig builds a Config from flag overrides, the process environment and
// dotenv files. Precedence: overrides > process env > dotenv files > defaults.
//
// overrides may be nil. Recognized keys: ONT_URL, ONT_USER, ONT_PASS,
// PING_TIMEOUT, DROP_TIMEOUT, REBOOT_MAX_TIME, ONT_PACE,
// ONT_PING_INTERVAL, ONT_DOWN_SLEEP, ONT_PING_HOST.
//
// Timeout keys accept plain seconds ("600") or Go durations ("10s", "2m").
func LoadConfig(overrides map[string]string, envFiles []string) (Config, error) {
	merged := map[string]string{}
	for _, f := range envFiles {
		m, err := LoadDotEnvFile(f)
		if err != nil {
			return Config{}, fmt.Errorf("read env file %s: %w", f, err)
		}
		applyDotEnv(merged, m)
	}
	// Process env wins over dotenv files.
	for k, v := range envSnapshot() {
		merged[k] = v
	}
	// Explicit overrides win over everything.
	for k, v := range overrides {
		if v != "" {
			merged[k] = v
		}
	}

	cfg := Config{
		URL:          DefaultONTURL,
		User:         DefaultONTUser,
		PingTimeout:  DefaultPingTimeout,
		DropTimeout:  DefaultDropTimeout,
		RebootMax:    DefaultRebootMax,
		Pace:         DefaultPace,
		PingInterval: DefaultPingInterval,
		DownSleep:    DefaultDownSleep,
		PingHost:     DefaultPingHost,
	}
	if v, ok := merged["ONT_URL"]; ok && strings.TrimSpace(v) != "" {
		cfg.URL = strings.TrimSpace(v)
	}
	if v, ok := merged["ONT_USER"]; ok && v != "" {
		cfg.User = v
	}
	if v, ok := merged["ONT_PASS"]; ok {
		cfg.Pass = v
	}
	if v, ok := merged["ONT_PING_HOST"]; ok && strings.TrimSpace(v) != "" {
		cfg.PingHost = strings.TrimSpace(v)
	}
	var err error
	if v, ok := merged["PING_TIMEOUT"]; ok && strings.TrimSpace(v) != "" {
		if cfg.PingTimeout, err = parseSecondsOrDuration(v, "PING_TIMEOUT"); err != nil {
			return Config{}, err
		}
	}
	if v, ok := merged["DROP_TIMEOUT"]; ok && strings.TrimSpace(v) != "" {
		if cfg.DropTimeout, err = parseSecondsOrDuration(v, "DROP_TIMEOUT"); err != nil {
			return Config{}, err
		}
	}
	if v, ok := merged["REBOOT_MAX_TIME"]; ok && strings.TrimSpace(v) != "" {
		if cfg.RebootMax, err = parseSecondsOrDuration(v, "REBOOT_MAX_TIME"); err != nil {
			return Config{}, err
		}
	}
	if v, ok := merged["ONT_PACE"]; ok && strings.TrimSpace(v) != "" {
		if cfg.Pace, err = parseSecondsOrDuration(v, "ONT_PACE"); err != nil {
			return Config{}, err
		}
	}
	if v, ok := merged["ONT_PING_INTERVAL"]; ok && strings.TrimSpace(v) != "" {
		if cfg.PingInterval, err = parseSecondsOrDuration(v, "ONT_PING_INTERVAL"); err != nil {
			return Config{}, err
		}
	}
	if v, ok := merged["ONT_DOWN_SLEEP"]; ok && strings.TrimSpace(v) != "" {
		if cfg.DownSleep, err = parseSecondsOrDuration(v, "ONT_DOWN_SLEEP"); err != nil {
			return Config{}, err
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// parseSecondsOrDuration accepts "600" (seconds) or a Go duration string.
func parseSecondsOrDuration(s, name string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%s is empty", name)
	}
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad %s %q: use seconds or Go duration", name, s)
	}
	return d, nil
}

// Validate checks required fields and sane timeouts.
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("ONT_URL is empty")
	}
	if strings.TrimSpace(c.User) == "" {
		return fmt.Errorf("ONT_USER is empty")
	}
	if c.Pass == "" {
		return fmt.Errorf("ONT_PASS is not set (copy .env.example to .env and fill it in)")
	}
	if c.PingTimeout <= 0 {
		return fmt.Errorf("PING_TIMEOUT must be > 0")
	}
	if c.DropTimeout < 0 {
		return fmt.Errorf("DROP_TIMEOUT must be >= 0")
	}
	if c.RebootMax <= 0 {
		return fmt.Errorf("REBOOT_MAX_TIME must be > 0")
	}
	if c.Pace < 0 {
		return fmt.Errorf("ONT_PACE must be >= 0")
	}
	if c.PingInterval <= 0 {
		return fmt.Errorf("ONT_PING_INTERVAL must be > 0")
	}
	if c.PingHost == "" {
		return fmt.Errorf("ONT_PING_HOST is empty")
	}
	return nil
}
