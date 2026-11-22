package http

import (
	"net"
	nethttp "net/http"
	"strings"

	"go.uber.org/zap"
)

// parseTrusted reads a comma-separated list of IPs and CIDRs. A bare IP is a
// single-address range. Entries that do not parse are logged and skipped.
func parseTrusted(list string, log *zap.Logger) []*net.IPNet {
	var out []*net.IPNet
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil {
				bits := 128
				if ip.To4() != nil {
					bits = 32
				}
				out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
				continue
			}
		}
		_, cidr, err := net.ParseCIDR(item)
		if err != nil {
			log.Error("ignoring an entry of server.trusted_proxies that is not an IP or CIDR", zap.String("entry", item))
			continue
		}
		out = append(out, cidr)
	}
	return out
}

func trusted(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// trustedRealIP replaces a request's RemoteAddr with the client address its
// proxy reports, but only when the connecting peer is a trusted proxy. Any
// other client could write those headers itself, and the address they carry
// is what webhook IP allowlists check. X-Forwarded-For is read from the right,
// skipping trusted hops, so a client cannot forge its address by prepending
// one; X-Real-IP is used when there is no X-Forwarded-For.
func trustedRealIP(nets []*net.IPNet) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if !trusted(net.ParseIP(host), nets) {
				next.ServeHTTP(w, r)
				return
			}
			if client := forwardedClient(r, nets); client != "" {
				r.RemoteAddr = client
			}
			next.ServeHTTP(w, r)
		})
	}
}

// forwardedClient returns the first untrusted address in X-Forwarded-For,
// read from the right, or X-Real-IP when that header is absent.
func forwardedClient(r *nethttp.Request, nets []*net.IPNet) string {
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		hops := strings.Split(strings.Join(xff, ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip == nil {
				return ""
			}
			if !trusted(ip, nets) {
				return ip.String()
			}
		}
		return ""
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return ""
}
