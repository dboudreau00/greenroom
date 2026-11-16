package templates

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Demo environments are private on purpose (127.0.0.1, 10.x,
// warehouse.internal), so private ranges are fine. Link-local and cloud
// metadata addresses are not: a probe pointed there reads instance
// credentials.
var (
	metadataNames = map[string]bool{
		"metadata": true, "metadata.google.internal": true,
		"instance-data": true, "instance-data.ec2.internal": true,
	}
	metadataPrefixes = []netip.Prefix{
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("fd00:ec2::254/128"),
	}
)

// metadataHost reports whether a param value names a link-local or cloud
// metadata host: as a URL with a host, as host:port, or as a bare host. The
// loose IPv4 forms (2852039166, 0xa9.254.169.254) count in a URL, and in
// host:port when the host has a dot, so a port number, a version or a
// time is not read as an address.
func metadataHost(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if u, err := url.Parse(v); err == nil && u.Host != "" && badHost(u.Hostname(), true) {
		return true
	}
	rest := v
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if h, _, err := net.SplitHostPort(rest); err == nil {
		if badHost(h, strings.Contains(v, "://") || strings.Contains(h, ".")) {
			return true
		}
	} else if rest != v && badHost(rest, strings.Contains(v, "://")) {
		return true
	}
	return badHost(v, false)
}

func badHost(h string, loose bool) bool {
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "" {
		return false
	}
	if metadataNames[h] {
		return true
	}
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i]
	}
	addr, err := netip.ParseAddr(h)
	if err != nil {
		var ok bool
		if addr, ok = looseIPv4(h); !loose || !ok {
			return false
		}
	}
	addr = addr.Unmap()
	for _, p := range metadataPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// looseIPv4 parses the forms some resolvers accept for an IPv4 address
// (inet_aton): one to four parts, each decimal, octal with a leading 0 or
// hex with 0x, so 2852039166 or 0251.0376.0251.0376 is caught too.
func looseIPv4(h string) (netip.Addr, bool) {
	parts := strings.Split(h, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 0, 32)
		if p == "" || err != nil || strings.HasPrefix(p, "+") {
			return netip.Addr{}, false
		}
		nums[i] = n
	}
	var v uint64
	last := len(nums) - 1
	for i, n := range nums[:last] {
		if n > 0xff {
			return netip.Addr{}, false
		}
		v |= n << (24 - 8*i)
	}
	if nums[last] >= 1<<(32-8*last) {
		return netip.Addr{}, false
	}
	v |= nums[last]
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}
