package sandbox

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// NetworkPolicy controls what a sandboxed script may reach over the network.
type NetworkPolicy string

const (
	// NetworkNone denies all outbound requests.
	NetworkNone NetworkPolicy = "none"
	// NetworkBogons allows public destinations only, rejecting private,
	// loopback, link-local, and other reserved ranges.
	NetworkBogons NetworkPolicy = "bogons"
	// NetworkFull allows any destination the host can reach.
	NetworkFull NetworkPolicy = "full"
)

// ParseNetworkPolicy resolves a configured policy name, defaulting to full for
// empty or unrecognized values so a typo never silently tightens behavior in a
// way that breaks running flows. Unknown values are reported to the caller.
func ParseNetworkPolicy(s string) (NetworkPolicy, error) {
	switch NetworkPolicy(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return NetworkFull, nil
	case NetworkNone:
		return NetworkNone, nil
	case NetworkBogons:
		return NetworkBogons, nil
	case NetworkFull:
		return NetworkFull, nil
	default:
		return NetworkFull, fmt.Errorf("unknown network policy %q; using \"full\"", s)
	}
}

// bogonPrefixes are the reserved ranges rejected under NetworkBogons that the
// netip.Addr helpers do not already classify. Loopback, private, link-local,
// multicast, and unspecified addresses are checked separately.
var bogonPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "This" network.
	netip.MustParsePrefix("100.64.0.0/10"),   // Carrier-grade NAT.
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments.
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1.
	netip.MustParsePrefix("192.88.99.0/24"),  // Deprecated 6to4 relay anycast.
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking.
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2.
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3.
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved for future use.
	netip.MustParsePrefix("::/128"),          // Unspecified.
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64.
	netip.MustParsePrefix("100::/64"),        // Discard-only.
	netip.MustParsePrefix("2001::/32"),       // Teredo.
	netip.MustParsePrefix("2001:10::/28"),    // ORCHID.
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation.
	netip.MustParsePrefix("2002::/16"),       // 6to4.
	netip.MustParsePrefix("fc00::/7"),        // Unique local.
	netip.MustParsePrefix("fe80::/10"),       // Link-local.
}

// IsBogon reports whether an address falls in a reserved range that scripts
// must not reach under the bogons policy. Metadata endpoints, LAN hosts, and
// the loopback interface all land here, which is the point: a script that is
// meant to talk to the internet has no business reaching the host's own
// services.
func IsBogon(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	// IPv4-mapped IPv6 addresses are classified as their IPv4 form so a
	// mapped 127.0.0.1 cannot slip past the IPv4 checks.
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range bogonPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ErrNetworkDenied is returned when the policy forbids an outbound request.
type ErrNetworkDenied struct {
	Reason string
}

func (e *ErrNetworkDenied) Error() string { return "network denied: " + e.Reason }

// HTTPClient builds an HTTP client that enforces the policy at connection
// time. The check runs in the dialer's Control hook, after DNS resolution and
// once per connection, so a hostname that resolves to a bogon is rejected no
// matter how it was spelled and DNS rebinding cannot move a connection to a
// blocked address after the check. Redirects go through the same dialer, so
// they are covered without extra handling.
func HTTPClient(policy NetworkPolicy, timeout time.Duration) *http.Client {
	if policy == NetworkNone {
		return &http.Client{Transport: deniedTransport{}}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if policy == NetworkBogons {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return &ErrNetworkDenied{Reason: "unparsable address " + address}
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return &ErrNetworkDenied{Reason: "unparsable address " + host}
			}
			if IsBogon(addr) {
				return &ErrNetworkDenied{Reason: host + " is in a reserved range"}
			}
			return nil
		}
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			DisableCompression:    false,
		},
		// Cap redirects so a redirect loop cannot burn the whole script budget.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
}

// deniedTransport fails every request under the "none" policy.
type deniedTransport struct{}

func (deniedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &ErrNetworkDenied{Reason: "network access is disabled by policy"}
}
