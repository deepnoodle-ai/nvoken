// Package httpguard provides HTTP clients for host-authored outbound URLs.
package httpguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

var (
	nonpublicRanges = []*net.IPNet{
		mustCIDR("0.0.0.0/8"),
		mustCIDR("100.64.0.0/10"),
		mustCIDR("192.0.0.0/24"),
		mustCIDR("192.0.2.0/24"),
		mustCIDR("192.88.99.0/24"),
		mustCIDR("198.18.0.0/15"),
		mustCIDR("198.51.100.0/24"),
		mustCIDR("203.0.113.0/24"),
		mustCIDR("240.0.0.0/4"),
		mustCIDR("64:ff9b::/96"),
		mustCIDR("64:ff9b:1::/48"),
		mustCIDR("100::/64"),
		mustCIDR("2001::/32"),
		mustCIDR("2001:2::/48"),
		mustCIDR("2001:db8::/32"),
		mustCIDR("2002::/16"),
		mustCIDR("fec0::/10"),
	}
)

func NewPublicClient(
	dnsTimeout time.Duration,
	connectTimeout time.Duration,
	tlsHandshakeTimeout time.Duration,
) *http.Client {
	return newPublicClient(
		dnsTimeout,
		connectTimeout,
		tlsHandshakeTimeout,
		net.DefaultResolver.LookupIPAddr,
	)
}

func newPublicClient(
	dnsTimeout time.Duration,
	connectTimeout time.Duration,
	tlsHandshakeTimeout time.Duration,
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error),
) *http.Client {
	if dnsTimeout <= 0 {
		dnsTimeout = 5 * time.Second
	}
	if connectTimeout <= 0 {
		connectTimeout = 5 * time.Second
	}
	if tlsHandshakeTimeout <= 0 {
		tlsHandshakeTimeout = 5 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if literal := net.ParseIP(host); literal != nil {
				if err := validatePublicIP(literal); err != nil {
					return nil, err
				}
				connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
				defer cancel()
				return dialer.DialContext(connectCtx, network, address)
			}
			dnsCtx, cancelDNS := context.WithTimeout(ctx, dnsTimeout)
			addresses, err := lookupIPAddr(dnsCtx, host)
			cancelDNS()
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("outbound target resolved to no addresses")
			}
			for _, resolved := range addresses {
				if err := validatePublicIP(resolved.IP); err != nil {
					return nil, err
				}
			}
			connectCtx, cancelConnect := context.WithTimeout(ctx, connectTimeout)
			defer cancelConnect()
			return dialer.DialContext(
				connectCtx,
				network,
				net.JoinHostPort(addresses[0].IP.String(), port),
			)
		},
		TLSHandshakeTimeout: tlsHandshakeTimeout,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("outbound redirects are not followed")
		},
	}
}

func validatePublicIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("outbound target is not resolvable")
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || isNonpublicRange(ip) {
		return fmt.Errorf("outbound target is not a public address")
	}
	return nil
}

func isNonpublicRange(ip net.IP) bool {
	for _, network := range nonpublicRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		panic(err)
	}
	return network
}
