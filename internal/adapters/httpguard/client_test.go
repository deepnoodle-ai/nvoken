package httpguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestPublicClientRejectsNonpublicAddressesAndRedirects(t *testing.T) {
	client := NewPublicClient(time.Second, time.Second, time.Second)
	transport := client.Transport.(*http.Transport)
	for _, address := range []string{
		"127.0.0.1:443",
		"10.0.0.1:443",
		"169.254.169.254:443",
		"100.64.0.1:443",
		"192.0.2.1:443",
		"198.18.0.1:443",
		"198.51.100.1:443",
		"203.0.113.1:443",
		"224.0.0.1:443",
		"240.0.0.1:443",
		"[::1]:443",
		"[64:ff9b::a00:1]:443",
		"[64:ff9b:1::a00:1]:443",
		"[100::1]:443",
		"[2001::1]:443",
		"[2001:2::1]:443",
		"[2001:db8::1]:443",
		"[2002:c000:0201::1]:443",
		"[fc00::1]:443",
		"[fec0::1]:443",
		"[fe80::1]:443",
		"[ff02::1]:443",
	} {
		if _, err := transport.DialContext(context.Background(), "tcp", address); err == nil {
			t.Fatalf("DialContext(%q) succeeded", address)
		}
	}
	if err := client.CheckRedirect(nil, nil); err == nil {
		t.Fatal("redirect was allowed")
	}
	if transport.Proxy != nil {
		t.Fatal("ambient proxy is enabled")
	}
}

func TestPublicClientRejectsMixedDNSAndBoundsLookup(t *testing.T) {
	client := newPublicClient(
		20*time.Millisecond,
		20*time.Millisecond,
		time.Second,
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("10.0.0.1")},
			}, nil
		},
	)
	if _, err := client.Transport.(*http.Transport).DialContext(
		context.Background(),
		"tcp",
		"example.com:443",
	); err == nil {
		t.Fatal("mixed public/private DNS result was allowed")
	}

	client = newPublicClient(
		20*time.Millisecond,
		20*time.Millisecond,
		time.Second,
		func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	_, err := client.Transport.(*http.Transport).DialContext(
		context.Background(),
		"tcp",
		"example.com:443",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DialContext error = %v", err)
	}
}
