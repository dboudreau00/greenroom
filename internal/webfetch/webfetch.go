// Package webfetch is the one HTTP client Greenroom uses for URLs that come
// from people rather than from configuration: a prospect's website for a
// brand kit, an asset link. It is built so that such a URL can not be used
// to reach anything but the public internet:
//
//   - only http and https, no credentials in the URL, only ports 80 and 443;
//   - every connection's actual peer address is checked at dial time, so a
//     host name that resolves (or re-resolves) to a loopback, private,
//     link-local, metadata or otherwise special address is refused;
//   - no proxy from the environment, which would hide the real destination;
//   - at most five redirects, each validated like the first URL;
//   - a hard cap on time and on response size.
//
// AllowPrivate lifts the address and port checks for workspaces that
// deliberately personalize from internal sites, and for tests.
package webfetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Options tune a client.
type Options struct {
	// AllowPrivate permits private, loopback and link-local destinations and
	// any port. Off by default.
	AllowPrivate bool
	// Timeout bounds a whole request including the body. Default 15s.
	Timeout time.Duration
	// MaxBytes caps a response body. Default 2 MiB.
	MaxBytes int64
	// UserAgent is sent with every request.
	UserAgent string
}

// Client fetches untrusted URLs.
type Client struct {
	opt Options
	hc  *http.Client
}

// Response is a fully read response.
type Response struct {
	URL         *url.URL
	Status      int
	ContentType string
	Body        []byte
}

// ErrBlocked is returned for a destination the client refuses to reach.
var ErrBlocked = errors.New("destination not allowed")

// New returns a client.
func New(opt Options) *Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 15 * time.Second
	}
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = 2 << 20
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "Greenroom link fetcher"
	}
	c := &Client{opt: opt}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: c.control}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    false,
	}
	c.hc = &http.Client{
		Transport: tr,
		Timeout:   opt.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return c.CheckURL(req.URL)
		},
	}
	return c
}

// CheckURL validates a URL before any connection is made.
func (c *Client) CheckURL(u *url.URL) error {
	if u == nil {
		return errors.New("no url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: only http and https links can be fetched", ErrBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in the link are not allowed", ErrBlocked)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("link has no host")
	}
	if c.opt.AllowPrivate {
		return nil
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return fmt.Errorf("%w: port %s (only 80 and 443)", ErrBlocked, p)
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	if addr, err := netip.ParseAddr(host); err == nil && !PublicAddr(addr) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, host)
	}
	return nil
}

// control runs after DNS resolution, on the address actually dialled.
func (c *Client) control(network, address string, _ syscall.RawConn) error {
	if c.opt.AllowPrivate {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlocked, address)
	}
	if !PublicAddr(ap.Addr()) {
		return fmt.Errorf("%w: %s resolves to a non-public address", ErrBlocked, ap.Addr())
	}
	if p := ap.Port(); p != 80 && p != 443 {
		return fmt.Errorf("%w: port %d", ErrBlocked, p)
	}
	return nil
}

// Special-purpose ranges beyond what netip classifies, from the IANA IPv4
// and IPv6 special-purpose address registries.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fec0::/10",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether addr is a globally routable unicast address.
func PublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// Get fetches rawURL. accept lists the media types the caller can use (for
// example "text/html" or "image/"); a response of any other type is
// refused. A prefix ending in "/" matches a whole family.
func (c *Client) Get(ctx context.Context, rawURL string, accept ...string) (*Response, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("bad link: %w", err)
	}
	if u.Scheme == "" {
		u, err = url.Parse("https://" + strings.TrimSpace(rawURL))
		if err != nil {
			return nil, fmt.Errorf("bad link: %w", err)
		}
	}
	if err := c.CheckURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opt.UserAgent)
	if len(accept) > 0 {
		req.Header.Set("Accept", strings.Join(accept, ", ")+", */*;q=0.1")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, resp.Request.URL.Host)
	}
	if resp.ContentLength > c.opt.MaxBytes {
		return nil, fmt.Errorf("response is %d bytes, the limit is %d", resp.ContentLength, c.opt.MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.opt.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.opt.MaxBytes {
		return nil, fmt.Errorf("response is larger than %d bytes", c.opt.MaxBytes)
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mt
	} else {
		ct = ""
	}
	if ct == "" || ct == "application/octet-stream" || ct == "binary/octet-stream" {
		sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(body))
		ct = sniffed
	}
	if len(accept) > 0 && !acceptable(ct, accept) {
		return nil, fmt.Errorf("unexpected content type %q", ct)
	}
	return &Response{URL: resp.Request.URL, Status: resp.StatusCode, ContentType: ct, Body: body}, nil
}

func acceptable(ct string, accept []string) bool {
	for _, a := range accept {
		if strings.HasSuffix(a, "/") {
			if strings.HasPrefix(ct, a) {
				return true
			}
		} else if ct == a {
			return true
		}
	}
	return false
}
