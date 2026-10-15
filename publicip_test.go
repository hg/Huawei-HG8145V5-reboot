package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFirstIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1.2.3.4", "1.2.3.4"},
		{`"1.2.3.4"`, "1.2.3.4"},
		{"prefix 5.6.7.8 suffix", "5.6.7.8"},
		{"two 1.1.1.1 and 8.8.8.8", "1.1.1.1"},
		{"no ip here", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstIPv4(tc.in); got != tc.want {
			t.Errorf("firstIPv4(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestPublicIP_OpenDNSPrimary(t *testing.T) {
	r := &PublicIPResolver{
		LookupA: func(ctx context.Context, host, server string) ([]string, error) {
			if host != opendnsName || server != opendnsServer {
				t.Fatalf("bad A query %q %q", host, server)
			}
			return []string{"203.0.113.7"}, nil
		},
		LookupTXT: func(ctx context.Context, host, server string) ([]string, error) {
			t.Fatal("TXT should not be called")
			return nil, nil
		},
		Timeout: time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "203.0.113.7" {
		t.Fatalf("got %q", got)
	}
}

func TestPublicIP_FallsBackToGoogle(t *testing.T) {
	r := &PublicIPResolver{
		LookupA: func(ctx context.Context, host, server string) ([]string, error) {
			return nil, errors.New("boom")
		},
		LookupTXT: func(ctx context.Context, host, server string) ([]string, error) {
			if host == googleName && server == googleServer {
				return []string{`"198.51.100.9"`}, nil
			}
			t.Fatalf("unexpected TXT %q %q", host, server)
			return nil, nil
		},
		Timeout: time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "198.51.100.9" {
		t.Fatalf("got %q", got)
	}
}

func TestPublicIP_FallsBackToCloudflare(t *testing.T) {
	calls := 0
	r := &PublicIPResolver{
		LookupA: func(ctx context.Context, host, server string) ([]string, error) {
			return []string{"not-an-ip"}, nil // no IPv4 -> fall through
		},
		LookupTXT: func(ctx context.Context, host, server string) ([]string, error) {
			calls++
			if host == googleName {
				return nil, errors.New("google down")
			}
			if host == cloudflareName && server == cloudflareServer {
				return []string{"192.0.2.55 extra"}, nil
			}
			return nil, errors.New("unexpected")
		},
		Timeout: time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "192.0.2.55" {
		t.Fatalf("got %q", got)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}

func TestPublicIP_Unknown(t *testing.T) {
	r := &PublicIPResolver{
		LookupA:   func(ctx context.Context, host, server string) ([]string, error) { return nil, errors.New("x") },
		LookupTXT: func(ctx context.Context, host, server string) ([]string, error) { return nil, errors.New("x") },
		Timeout:   time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}

func TestPublicIP_NilLookups(t *testing.T) {
	r := &PublicIPResolver{}
	if got := r.PublicIP(context.Background()); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}

func TestPublicIP_EmptyAddrs(t *testing.T) {
	r := &PublicIPResolver{
		LookupA:   func(ctx context.Context, host, server string) ([]string, error) { return []string{}, nil },
		LookupTXT: func(ctx context.Context, host, server string) ([]string, error) { return []string{""}, nil },
		Timeout:   time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}

func TestPublicIP_MultipleAddrsPicksFirstIPv4(t *testing.T) {
	r := &PublicIPResolver{
		LookupA: func(ctx context.Context, host, server string) ([]string, error) {
			return []string{"junk", "10.20.30.40"}, nil
		},
		LookupTXT: nil,
		Timeout:   time.Second,
	}
	if got := r.PublicIP(context.Background()); got != "10.20.30.40" {
		t.Fatalf("got %q", got)
	}
}
