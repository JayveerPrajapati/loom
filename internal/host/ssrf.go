package host

import (
	"net"
	"strings"
)

// blockedHost rejects literal private targets; safeDialer also validates DNS
// results and redirects.
func blockedHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return blockedIP(ip)
	}
	return false
}

func blockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		isCGNAT(ip) {
		return true
	}
	// NAT64 and 6to4 addresses embed an IPv4 destination; the embedded
	// target is what actually gets reached, so it must pass the same guard
	// whether or not the deployment network can route the translation.
	if embedded := embeddedIPv4(ip); embedded != nil && blockedIP(embedded) {
		return true
	}
	return false
}

// isCGNAT reports whether ip is in RFC 6598's 100.64.0.0/10 shared address
// space. Overlay networks and some cloud metadata services live there, so it
// is private for SSRF purposes even though Go's IsPrivate does not cover it.
func isCGNAT(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return false
}

// embeddedIPv4 extracts the IPv4 destination carried by a NAT64
// (64:ff9b::/96, RFC 6052 well-known prefix) or 6to4 (2002::/16, RFC 3056)
// address. It returns nil for plain IPv4 (including IPv4-mapped IPv6, which
// Go normalizes) and for addresses that embed no IPv4 target.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return nil
	}
	// 64:ff9b:0:0:0:0 followed by the 32-bit IPv4 target.
	if ip16[0] == 0x00 && ip16[1] == 0x64 && ip16[2] == 0xff && ip16[3] == 0x9b {
		for _, b := range ip16[4:12] {
			if b != 0 {
				return nil
			}
		}
		return net.IP(append([]byte(nil), ip16[12:16]...))
	}
	// 2002:<first 16 bits of IPv4>:<next 16 bits>:: — the embedded target is
	// bits 16..47.
	if ip16[0] == 0x20 && ip16[1] == 0x02 {
		return net.IP(append([]byte(nil), ip16[2:6]...))
	}
	return nil
}
