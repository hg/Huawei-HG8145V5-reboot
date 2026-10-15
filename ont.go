package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Reboot paths exactly as used by reconnect.sh and the captured web UI.
const (
	randCountPath = "/asp/GetRandCount.asp"
	loginPath     = "/login.cgi"
	mgmtPath      = "/html/ssmp/accoutcfg/ontmngt.asp"
	rebootPath    = "/html/ssmp/accoutcfg/set.cgi"
	rebootQuery   = "x=InternetGatewayDevice.X_HW_DEBUG.SMP.DM.ResetBoard&RequestFile=html/ssmp/accoutcfg/ontmngt.asp"
	loginSuccess  = "pageName = 'index.asp'"
)

// RebootOutcome classifies the reboot POST result.
type RebootOutcome string

const (
	// RebootClean means the ONT answered HTTP 2xx/3xx before going down.
	RebootClean RebootOutcome = "clean"
	// RebootReset means the connection was reset/EOF mid-POST: the ONT dies with its HTTP server.
	RebootReset RebootOutcome = "reset"
	// RebootTimeout means the POST hit RebootMax: the ONT hangs the response.
	RebootTimeout RebootOutcome = "timeout"
)

// ONTClient speaks the HG8145V5 web protocol with ida/net/http only.
// It keeps a cookie jar like curl's -c/-b (the real ONT never sets cookies;
// the session is tracked by client IP — see FINDINGS.md).
type ONTClient struct {
	BaseURL   string
	HTTP      *http.Client
	ConnectTO time.Duration
	RebootMax time.Duration
	MaxBody   int64
}

// NewONTClient builds a client with a cookie jar, redirect following (like
// curl -L) and a 10s dial timeout (like curl --connect-timeout 10).
func NewONTClient(baseURL string, rebootMax time.Duration) (*ONTClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("empty ONT URL")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("bad ONT URL: %w", err)
	}
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   false,
		DisableKeepAlives:   false,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &ONTClient{
		BaseURL:   baseURL,
		HTTP:      &http.Client{Jar: jar, Transport: transport, CheckRedirect: nil},
		ConnectTO: 10 * time.Second,
		RebootMax: rebootMax,
		MaxBody:   4 << 20,
	}, nil
}

// IsHexToken reports whether s looks like an ONT token: >=32 lowercase hex
// chars. Mirrors [[ $TOKEN =~ ^[0-9a-f]{32,}$ ]] in reconnect.sh.
func IsHexToken(s string) bool {
	if len(s) < 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// CleanRandCount strips the BOM prefix (U+FEFF, i.e. EF BB BF bytes)
// and trailing CR/LF/whitespace, mirroring tr -d CR/LF/BOM.
func CleanRandCount(raw string) string {
	s := strings.TrimPrefix(raw, "\uFEFF")
	s = strings.Trim(s, "\r\n \t\uFEFF")
	// tr -d removes ALL \r \n anywhere, not just edges.
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\uFEFF", "")
	return strings.TrimSpace(s)
}

// GetLoginPage performs GET / (curl -fsSL -c/-b JAR $URL/ -o /dev/null).
// Any 4xx/5xx is an error, mirroring curl -f.
func (c *ONTClient) GetLoginPage(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET login page: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxBody()))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GET login page: HTTP %s", resp.Status)
	}
	return nil
}

// FetchLoginToken performs POST /asp/GetRandCount.asp and validates the token.
func (c *ONTClient) FetchLoginToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+randCountPath, nil)
	if err != nil {
		return "", err
	}
	req.ContentLength = 0
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch login token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody()))
	if err != nil {
		return "", fmt.Errorf("fetch login token: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch login token: HTTP %s", resp.Status)
	}
	token := CleanRandCount(string(body))
	if !IsHexToken(token) {
		return "", fmt.Errorf("bad login token: %q", truncate(token, 80))
	}
	return token, nil
}

// EncodePassword base64-encodes the password like `printf %s $PASS | base64`.
func EncodePassword(pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(pass))
}

// Login POSTs /login.cgi and requires pageName = 'index.asp' in the reply.
func (c *ONTClient) Login(ctx context.Context, user, pass, token string) error {
	form := url.Values{}
	form.Set("UserName", user)
	form.Set("PassWord", EncodePassword(pass))
	form.Set("Language", "english")
	form.Set("x.X_HW_Token", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+loginPath,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody()))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("login: HTTP %s", resp.Status)
	}
	if !strings.Contains(string(body), loginSuccess) {
		return fmt.Errorf("login failed: %s", truncate(string(body), 200))
	}
	return nil
}

// FetchOntToken GETs ontmngt.asp and scrapes the hidden onttoken field.
func (c *ONTClient) FetchOntToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+mgmtPath, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch onttoken: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody()))
	if err != nil {
		return "", fmt.Errorf("fetch onttoken: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch onttoken: HTTP %s", resp.Status)
	}
	token, err := ExtractOntToken(string(body))
	if err != nil {
		return "", err
	}
	return token, nil
}

// ExtractOntToken scrapes value="..." from the name="onttoken" element.
// It mirrors: grep -o -E 'name="onttoken"[^>]*' | grep -o -E 'value="[0-9a-f]+"'
// while also tolerating value-before-name order and single quotes.
func ExtractOntToken(html string) (string, error) {
	// Fast path mirroring the shell pipeline.
	if tok := extractNameFirst(html); tok != "" {
		if !IsHexToken(tok) {
			return "", fmt.Errorf("onttoken not found (session dead?)")
		}
		return tok, nil
	}
	if tok := extractValueFirst(html); tok != "" {
		if !IsHexToken(tok) {
			return "", fmt.Errorf("onttoken not found (session dead?)")
		}
		return tok, nil
	}
	return "", fmt.Errorf("onttoken not found (session dead?)")
}

// extractNameFirst finds name="onttoken" then value="..." after it.
func extractNameFirst(html string) string {
	for _, nameSpelling := range []string{`name="onttoken"`, `name='onttoken'`} {
		idx := strings.Index(html, nameSpelling)
		if idx < 0 {
			continue
		}
		rest := html[idx:]
		// Cut at the end of the tag.
		if end := strings.IndexByte(rest, '>'); end >= 0 {
			rest = rest[:end]
		} else if len(rest) > 4096 {
			rest = rest[:4096]
		}
		if v, ok := attrValue(rest, "value"); ok {
			return v
		}
	}
	return ""
}

// extractValueFirst handles <... value="..." ... name="onttoken" ...>.
func extractValueFirst(html string) string {
	// Scan each tag containing onttoken.
	search := html
	for {
		idx := strings.Index(search, "onttoken")
		if idx < 0 {
			return ""
		}
		start := strings.LastIndex(search[:idx], "<")
		if start < 0 {
			search = search[idx+len("onttoken"):]
			continue
		}
		end := strings.Index(search[start:], ">")
		if end < 0 {
			return ""
		}
		tag := search[start : start+end+1]
		if strings.Contains(tag, "name=") {
			if v, ok := attrValue(tag, "value"); ok {
				return v
			}
		}
		search = search[idx+len("onttoken"):]
	}
}

// attrValue extracts attr="..." or attr='...' from a tag fragment.
func attrValue(tag, attr string) (string, bool) {
	for _, q := range []string{`"`, `'`} {
		key := attr + "=" + q
		idx := strings.Index(tag, key)
		if idx < 0 {
			continue
		}
		rest := tag[idx+len(key):]
		if end := strings.Index(rest, q); end >= 0 {
			return rest[:end], true
		}
	}
	return "", false
}

// Reboot POSTs the ResetBoard command. The ONT hangs the response, so a
// reset (connection reset/EOF) or a timeout (capped at RebootMax) is the
// expected success signal — mirroring curl rc 56 / 28 handling.
func (c *ONTClient) Reboot(ctx context.Context, ontToken string) (RebootOutcome, error) {
	max := c.RebootMax
	if max <= 0 {
		max = DefaultRebootMax
	}
	ctx, cancel := context.WithTimeout(ctx, max)
	defer cancel()

	target := c.BaseURL + rebootPath + "?" + rebootQuery
	form := url.Values{}
	form.Set("x.X_HW_Token", ontToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return classifyRebootError(ctx, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxBody()))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("reboot POST failed: HTTP %s", resp.Status)
	}
	return RebootClean, nil
}

// classifyRebootError maps transport errors to reboot outcomes.
// Timeouts (context deadline or net timeout) -> RebootTimeout (curl 28).
// Resets/EOFs -> RebootReset (curl 56). Anything else is fatal.
func classifyRebootError(ctx context.Context, err error) (RebootOutcome, error) {
	msg := strings.ToLower(err.Error())
	// Context deadline is unambiguous: our max-time fired.
	if ctx.Err() == context.DeadlineExceeded {
		return RebootTimeout, nil
	}
	var nerr net.Error
	if ok := asNetError(err, &nerr); ok && nerr.Timeout() {
		return RebootTimeout, nil
	}
	if strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "client timeout") ||
		strings.Contains(msg, "timeout") && strings.Contains(msg, "context") {
		return RebootTimeout, nil
	}
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "eof") {
		return RebootReset, nil
	}
	return "", fmt.Errorf("reboot POST failed: %w", err)
}

func asNetError(err error, target *net.Error) bool {
	type causer interface{ Unwrap() error }
	type multiUnwrapper interface{ Unwrap() []error }
	// Manual unwrap loop to avoid importing errors (still stdlib, but keep it simple).
	for err != nil {
		if ne, ok := err.(net.Error); ok {
			*target = ne
			return true
		}
		if uw, ok := err.(causer); ok {
			err = uw.Unwrap()
			continue
		}
		if uw, ok := err.(multiUnwrapper); ok {
			for _, e := range uw.Unwrap() {
				var t net.Error
				if asNetError(e, &t) {
					*target = t
					return true
				}
			}
			return false
		}
		return false
	}
	return false
}

func (c *ONTClient) maxBody() int64 {
	if c.MaxBody > 0 {
		return c.MaxBody
	}
	return 4 << 20
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
