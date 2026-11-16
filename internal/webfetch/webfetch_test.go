package webfetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.215.14", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.5.4", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "0.1.2.3",
		"224.0.0.1", "255.255.255.255", "192.0.2.10", "198.18.0.1", "240.0.0.1",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "2002:a00:1::1", "2001:db8::1", "ff02::1",
	} {
		if PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
}

func TestCheckURL(t *testing.T) {
	c := New(Options{})
	for _, raw := range []string{
		"file:///etc/passwd", "gopher://x", "ftp://example.com/", "https://user:pw@example.com/", "http://localhost/", "http://app.localhost/",
		"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data/", "http://[::1]/", "http://10.0.0.1/", "https://example.com:8443/", "http://example.com:22/",
	} {
		u, _ := url.Parse(raw)
		if err := c.CheckURL(u); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
	for _, raw := range []string{"https://example.com/", "http://example.com:80/x", "https://example.com:443/"} {
		u, _ := url.Parse(raw)
		if err := c.CheckURL(u); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
	open := New(Options{AllowPrivate: true})
	u, _ := url.Parse("http://127.0.0.1:8080/")
	if err := open.CheckURL(u); err != nil {
		t.Errorf("allowPrivate refused loopback: %v", err)
	}
	u, _ = url.Parse("file:///etc/passwd")
	if err := open.CheckURL(u); err == nil {
		t.Error("allowPrivate accepted file://")
	}
}

// The dial-time check is what stops a public name that resolves to a
// private address, including after a redirect.
func TestDialControl(t *testing.T) {
	c := New(Options{})
	for _, addr := range []string{"127.0.0.1:443", "10.0.0.5:80", "[::1]:443", "169.254.169.254:80", "93.184.215.14:8080"} {
		if err := c.control("tcp", addr, nil); err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if err := c.control("tcp", "93.184.215.14:443", nil); err != nil {
		t.Errorf("public 443 refused: %v", err)
	}
}

func TestGetGuardsAndLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html><title>x</title></html>")
		case "/big":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(strings.Repeat("a", 4096)))
		case "/hop":
			http.Redirect(w, r, "/hop", http.StatusFound)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	// The test server listens on loopback: refused unless private is allowed.
	if _, err := New(Options{}).Get(ctx, srv.URL+"/page", "text/html"); err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback fetched without allowPrivate: %v", err)
	}
	c := New(Options{AllowPrivate: true, MaxBytes: 1024})
	resp, err := c.Get(ctx, srv.URL+"/page", "text/html")
	if err != nil || resp.ContentType != "text/html" || !strings.Contains(string(resp.Body), "<title>") {
		t.Fatalf("page: %v %+v", err, resp)
	}
	if _, err := c.Get(ctx, srv.URL+"/big", "text/html"); err == nil || !strings.Contains(err.Error(), "larger") && !strings.Contains(err.Error(), "limit") {
		t.Fatalf("size limit: %v", err)
	}
	if _, err := c.Get(ctx, srv.URL+"/hop", "text/html"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect loop: %v", err)
	}
	if _, err := c.Get(ctx, srv.URL+"/json", "text/html"); err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("content type: %v", err)
	}
	if _, err := c.Get(ctx, srv.URL+"/missing", "text/html"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
}
