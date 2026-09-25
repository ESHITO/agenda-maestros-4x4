package server

import (
	"net/http"
	"net/netip"
)

// Fork (Agenda Maestros 4x4): the bucket key of the WhatsApp short links' rate limit.

// shortLinkClientKey is the rate-limit bucket of a request to /e/{code} or /c/{code}: the
// client IP as remoteIP resolves it (TCP peer, or the client TRUSTED_PROXY_CIDRS vouches
// for), with an IPv6 address cut to its /64.
//
// The limit is what bounds guessing a ~40-bit code, and a single IPv6 host is normally
// handed a whole /64 (a cheap VPS included): keyed per /128 it could take a fresh address,
// and a fresh budget, for every request. A /64 is one site, so it gets one budget. IPv4
// (IPv4-mapped IPv6 included) stays per address. Anything unparseable is used as it is.
func shortLinkClientKey(r *http.Request) string {
	ip := remoteIP(r)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
