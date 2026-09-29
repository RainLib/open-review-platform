package providertransport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// NewClient keeps deployment-owned provider credentials on the admitted host.
// It never uses proxy environment variables or follows redirects, and resolves
// the connection address itself so a DNS result cannot be swapped at dial time.
func NewClient(allowPrivateNetworks bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !allowPrivateNetworks && (!ip.IsGlobalUnicast() || ip.IsPrivate()) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			return nil, fmt.Errorf("provider destination has no permitted address")
		},
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
