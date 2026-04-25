package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/MemaxLabs/memax-go-agent-sdk/web"
)

const (
	defaultWebFetchMaxBytes  = 512 * 1024
	maxWebFetchMaxBytes      = 4 * 1024 * 1024
	defaultWebFetchTimeout   = 20 * time.Second
	defaultWebFetchRedirects = 5
)

var htmlTitlePattern = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)

// HTTPFetcherConfig controls the CLI's default web_fetch backend.
type HTTPFetcherConfig struct {
	MaxBytes            int
	Timeout             time.Duration
	MaxRedirects        int
	AllowPrivateNetwork bool
}

// HTTPFetcher is the CLI's default bounded HTTP(S) web fetch backend.
type HTTPFetcher struct {
	maxBytes            int
	timeout             time.Duration
	maxRedirects        int
	allowPrivateNetwork bool
	client              *http.Client
}

// NewHTTPFetcher returns a bounded HTTP(S) fetcher suitable for the default CLI
// web_fetch tool. The default policy blocks loopback, link-local, and private
// network addresses; command tools remain available for explicit local network
// checks when the user asks for them.
func NewHTTPFetcher(config HTTPFetcherConfig) *HTTPFetcher {
	maxBytes := config.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultWebFetchMaxBytes
	}
	if maxBytes > maxWebFetchMaxBytes {
		maxBytes = maxWebFetchMaxBytes
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultWebFetchTimeout
	}
	maxRedirects := config.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultWebFetchRedirects
	}
	fetcher := &HTTPFetcher{
		maxBytes:            maxBytes,
		timeout:             timeout,
		maxRedirects:        maxRedirects,
		allowPrivateNetwork: config.AllowPrivateNetwork,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The default web fetch policy is enforced by this process. Do not inherit
	// proxy environment variables here: a proxy would perform its own DNS
	// resolution and could bypass the private-network guard below. Users can
	// still run explicit proxy or local-network checks through command tools.
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: timeout}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if fetcher.allowPrivateNetwork {
			return dialer.DialContext(ctx, network, address)
		}
		// This dial-time resolution is the security boundary. FetchURL and
		// redirects do preflight validation for clearer errors, but the dialer
		// must still pin the connection to a vetted IP literal.
		ips, err := fetcher.resolvePublicIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	fetcher.client = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetcher.maxRedirects {
				return fmt.Errorf("stopped after %d redirects", fetcher.maxRedirects)
			}
			if err := validateHTTPURL(req.URL); err != nil {
				return err
			}
			req.URL.User = nil
			return fetcher.validateHost(req.Context(), req.URL.Hostname())
		},
	}
	return fetcher
}

// FetchURL fetches req.URL through the default CLI HTTP client.
func (f *HTTPFetcher) FetchURL(ctx context.Context, req web.FetchRequest) (web.FetchResult, error) {
	if f == nil {
		return web.FetchResult{}, fmt.Errorf("web fetcher is nil")
	}
	parsed, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return web.FetchResult{}, fmt.Errorf("parse url: %w", err)
	}
	parsed.User = nil
	if err := validateHTTPURL(parsed); err != nil {
		return web.FetchResult{}, err
	}
	if err := f.validateHost(ctx, parsed.Hostname()); err != nil {
		return web.FetchResult{}, err
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > f.maxBytes {
		maxBytes = f.maxBytes
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return web.FetchResult{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "memax-code/0")
	httpReq.Header.Set("Accept", "text/html, text/plain, application/json, application/xml;q=0.9, */*;q=0.5")

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return web.FetchResult{}, fmt.Errorf("fetch %s: %w", parsed.String(), err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, int64(maxBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return web.FetchResult{}, fmt.Errorf("read %s: %w", parsed.String(), err)
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	content := string(body)
	metadata := map[string]any{
		"truncated": truncated,
	}
	return web.FetchResult{
		URL:         parsed.String(),
		FinalURL:    resp.Request.URL.String(),
		Title:       extractHTMLTitle(content),
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
		FetchedAt:   time.Now().UTC(),
		Metadata:    metadata,
	}, nil
}

func validateHTTPURL(parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("url is required")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("url scheme must be http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

func (f *HTTPFetcher) validateHost(ctx context.Context, host string) error {
	_, err := f.resolvePublicIPs(ctx, host)
	return err
}

func (f *HTTPFetcher) resolvePublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if host == "" {
		return nil, fmt.Errorf("url must include a host")
	}
	if f.allowPrivateNetwork {
		return nil, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateAddress(ip) {
			return nil, fmt.Errorf("web fetch blocked private address %s", ip)
		}
		return []net.IP{ip}, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if isPrivateAddress(ip) {
			return nil, fmt.Errorf("web fetch blocked private address %s for host %s", ip, host)
		}
	}
	return ips, nil
}

func isPrivateAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

func extractHTMLTitle(content string) string {
	matches := htmlTitlePattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return ""
	}
	title := matches[1]
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}
