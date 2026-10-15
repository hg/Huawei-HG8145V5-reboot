package main

import (
	"context"
	"net"
	"regexp"
	"time"
)

// Public-IP discovery mirrors reconnect.sh's public_ip():
//  1. A myip.opendns.com @resolver1.opendns.com
//  2. TXT o-o.myaddr.l.google.com @ns1.google.com
//  3. TXT whoami.cloudflare.com @ns3.cloudflare.com
//
// otherwise "unknown".
const (
	opendnsName      = "myip.opendns.com"
	opendnsServer    = "resolver1.opendns.com:53"
	googleName       = "o-o.myaddr.l.google.com"
	googleServer     = "ns1.google.com:53"
	cloudflareName   = "whoami.cloudflare.com"
	cloudflareServer = "ns3.cloudflare.com:53"
	unknownIP        = "unknown"
)

var ipv4Re = regexp.MustCompile(`[0-9]{1,3}(\.[0-9]{1,3}){3}`)

// firstIPv4 returns the first IPv4-looking substring, like
// grep -Eo '[0-9]{1,3}(\.[0-9]{1,3}){3}' | head -n 1.
func firstIPv4(s string) string {
	return ipv4Re.FindString(s)
}

// HostLookup resolves A/AAAA records for host via the given DNS server
// (host:port). TXTLookup resolves TXT records via the given DNS server.
type HostLookup func(ctx context.Context, host, server string) ([]string, error)
type TXTLookup func(ctx context.Context, host, server string) ([]string, error)

// PublicIPResolver discovers the public IP with injectable DNS lookups
// (tests stub these; production uses LookupHostVia / LookupTXTComprisedVia).
type PublicIPResolver struct {
	LookupA   HostLookup
	LookupTXT TXTLookup
	Timeout   time.Duration
}

// NewPublicIPResolver returns a resolver using the system-independent
// Go resolver dialed at the specific servers (pure stdlib, no drill needed).
func NewPublicIPResolver() *PublicIPResolver {
	return &PublicIPResolver{
		LookupA:   LookupHostVia,
		LookupTXT: LookupTXTComprisedVia,
		Timeout:   8 * time.Second,
	}
}

// PublicIP returns the public IP or "unknown" (never errors, like the shell).
func (r *PublicIPResolver) PublicIP(ctx context.Context) string {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	// 1. OpenDNS A query.
	if ip := r.opendns(ctx, timeout); ip != "" {
		return ip
	}
	// 2. Google TXT query.
	if ip := r.google(ctx, timeout); ip != "" {
		return ip
	}
	// 3. Cloudflare TXT query.
	if ip := r.cloudflare(ctx, timeout); ip != "" {
		return ip
	}
	return unknownIP
}

func (r *PublicIPResolver) opendns(ctx context.Context, timeout time.Duration) string {
	if r.LookupA == nil {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addrs, err := r.LookupA(c, opendnsName, opendnsServer)
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ip := firstIPv4(a); ip != "" {
			return ip
		}
	}
	return ""
}

func (r *PublicIPResolver) google(ctx context.Context, timeout time.Duration) string {
	if r.LookupTXT == nil {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	txts, err := r.LookupTXT(c, googleName, googleServer)
	if err != nil {
		return ""
	}
	for _, t := range txts {
		if ip := firstIPv4(t); ip != "" {
			return ip
		}
	}
	return ""
}

func (r *PublicIPResolver) cloudflare(ctx context.Context, timeout time.Duration) string {
	if r.LookupTXT == nil {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	txts, err := r.LookupTXT(c, cloudflareName, cloudflareServer)
	if err != nil {
		return ""
	}
	for _, t := range txts {
		if ip := firstIPv4(t); ip != "" {
			return ip
		}
	}
	return ""
}

// LookupHostVia resolves host using server (host:port) via the pure-Go
// resolver with a custom dialer. No drill/binary needed.
func LookupHostVia(ctx context.Context, host, server string) ([]string, error) {
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	return res.LookupHost(ctx, host)
}

// LookupTXTComprisedVia resolves TXT records for host via server.
func LookupTXTComprisedVia(ctx context.Context, host, server string) ([]string, error) {
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	return res.LookupTXT(ctx, host)
}
