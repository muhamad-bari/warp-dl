package downloader

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

const cloudflareDoH = "https://cloudflare-dns.com/dns-query"

type doHAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	TTL  int    `json:"TTL"`
	Data string `json:"data"`
}

type doHResponse struct {
	Status int         `json:"Status"`
	Answer []doHAnswer `json:"Answer"`
}

// NewDoHTransport returns a custom http.Transport that uses DoH for DNS resolution
func NewDoHTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Check if host is already an IP
			if net.ParseIP(host) != nil {
				var d net.Dialer
				d.Timeout = 30 * time.Second
				d.KeepAlive = 30 * time.Second
				return d.DialContext(ctx, network, addr)
			}

			// Resolve IP via DoH
			ip, err := resolveDoH(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DoH resolution failed for %s: %w", host, err)
			}

			// Dial the resolved IP directly
			targetAddr := net.JoinHostPort(ip, port)
			var d net.Dialer
			d.Timeout = 30 * time.Second
			d.KeepAlive = 30 * time.Second
			return d.DialContext(ctx, network, targetAddr)
		},
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func resolveDoH(ctx context.Context, domain string) (string, error) {
	// Use 1.1.1.1 directly for the DoH request to avoid system DNS lookup for cloudflare-dns.com
	// However, TLS verification might fail if we use IP in URL without proper Host header or if cert doesn't match IP.
	// Cloudflare's cert is valid for cloudflare-dns.com.
	// Common practice: Resolve cloudflare-dns.com once using system DNS (usually allowed) or hardcode IP.
	// Let's rely on system DNS for the initial bootstrap of the DoH provider itself, 
	// assuming the ISP blocks specific sites, not Cloudflare's public DNS service.
	
	req, err := http.NewRequestWithContext(ctx, "GET", cloudflareDoH, nil)
	if err != nil {
		return "", err
	}

	q := req.URL.Query()
	q.Add("name", domain)
	q.Add("type", "A") // IPv4 only for simplicity
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/dns-json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// Use a clean client for the DNS query
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DoH server returned status: %s", resp.Status)
	}

	var dohResp doHResponse
	if err := json.NewDecoder(resp.Body).Decode(&dohResp); err != nil {
		return "", err
	}

	if dohResp.Status != 0 {
		return "", fmt.Errorf("DNS error code: %d", dohResp.Status)
	}

	if len(dohResp.Answer) == 0 {
		return "", fmt.Errorf("no DNS answer found for %s", domain)
	}

	// Return the first A record (Type 1)
	for _, ans := range dohResp.Answer {
		if ans.Type == 1 {
			return ans.Data, nil
		}
	}

	return "", fmt.Errorf("no A record found for %s", domain)
}
