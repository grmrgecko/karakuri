// Package socket owns shared TCP, UDP and WebSocket connections.
package socket

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// MaxBytes bounds frames and request responses held in memory.
const MaxBytes = 1 << 20

// Config describes one immutable connection generation.
type Config struct {
	Mode                                      string   `json:"mode"`
	Address                                   string   `json:"address"`
	URL                                       string   `json:"url"`
	Path                                      string   `json:"path"`
	Framing                                   string   `json:"framing"`
	Delimiter                                 string   `json:"delimiter"`
	IncludeDelimiter                          bool     `json:"include_delimiter"`
	FixedLength                               int      `json:"fixed_length"`
	MaxBytes                                  int      `json:"max_bytes"`
	MaxSessions                               int      `json:"max_sessions"`
	DialTimeout                               string   `json:"dial_timeout"`
	WriteTimeout                              string   `json:"write_timeout"`
	ReconnectDelay                            string   `json:"reconnect_delay"`
	TLS                                       bool     `json:"tls"`
	CA                                        string   `json:"ca"`
	Certificate                               string   `json:"certificate"`
	PrivateKey                                string   `json:"private_key"`
	ServerName                                string   `json:"server_name"`
	Family                                    string   `json:"family"`
	Destination                               string   `json:"destination"`
	MulticastGroup                            string   `json:"multicast_group"`
	Interface                                 string   `json:"interface"`
	TTL                                       int      `json:"ttl"`
	Loopback                                  *bool    `json:"loopback"`
	Broadcast                                 bool     `json:"broadcast"`
	HeadersJSON                               string   `json:"headers_json"`
	Token                                     string   `json:"token"`
	Origins                                   []string `json:"origins"`
	Subprotocols                              []string `json:"subprotocols"`
	dialTimeout, writeTimeout, reconnectDelay time.Duration
	tlsConfig                                 *tls.Config
	headers                                   http.Header
}

// ParseConfig validates protocol settings before any socket is opened.
func ParseConfig(kind string, raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 65536
		if kind == "udp" {
			c.MaxBytes = 65507
		}
	}
	cap := MaxBytes
	if kind == "udp" {
		cap = 65507
	}
	if c.MaxBytes < 1 || c.MaxBytes > cap {
		return c, fmt.Errorf("max_bytes must be between 1 and %d", cap)
	}
	if c.MaxSessions == 0 {
		c.MaxSessions = 16
	}
	if c.MaxSessions < 1 || c.MaxSessions > 64 {
		return c, fmt.Errorf("max_sessions must be between 1 and 64")
	}
	var err error
	if c.dialTimeout, err = duration(c.DialTimeout, 10*time.Second); err != nil {
		return c, fmt.Errorf("dial timeout: %w", err)
	}
	if c.writeTimeout, err = duration(c.WriteTimeout, 10*time.Second); err != nil {
		return c, fmt.Errorf("write timeout: %w", err)
	}
	if c.reconnectDelay, err = duration(c.ReconnectDelay, time.Second); err != nil {
		return c, fmt.Errorf("reconnect delay: %w", err)
	}
	switch kind {
	case "tcp":
		if c.Mode == "" {
			c.Mode = "client"
		}
		if c.Mode != "client" && c.Mode != "listener" && c.Mode != "request" {
			return c, fmt.Errorf("invalid tcp mode")
		}
		if _, _, err = net.SplitHostPort(c.Address); err != nil {
			return c, fmt.Errorf("address must be host:port: %w", err)
		}
		if c.Framing == "" {
			c.Framing = "delimiter"
		}
		if c.Delimiter == "" {
			c.Delimiter = `\n`
		}
		if err = (Framing{Mode: c.Framing, Delimiter: c.Delimiter, Length: c.FixedLength, MaxBytes: c.MaxBytes}).Validate(false); err != nil {
			return c, err
		}
	case "udp":
		if c.Family == "" {
			c.Family = "udp4"
		}
		if c.Family != "udp4" && c.Family != "udp6" {
			return c, fmt.Errorf("family must be udp4 or udp6")
		}
		if c.Address == "" {
			if c.Family == "udp6" {
				c.Address = "[::1]:0"
			} else {
				c.Address = "127.0.0.1:0"
			}
		}
		if _, err = net.ResolveUDPAddr(c.Family, c.Address); err != nil {
			return c, fmt.Errorf("bind address: %w", err)
		}
		if c.Destination != "" {
			if _, _, err = net.SplitHostPort(c.Destination); err != nil {
				return c, fmt.Errorf("destination must be host:port")
			}
		}
		if c.TTL == 0 {
			c.TTL = 1
		}
		if c.TTL < 1 || c.TTL > 255 {
			return c, fmt.Errorf("ttl must be between 1 and 255")
		}
		if c.MulticastGroup != "" {
			ip := net.ParseIP(c.MulticastGroup)
			if ip == nil || !ip.IsMulticast() || (ip.To4() != nil) != (c.Family == "udp4") {
				return c, fmt.Errorf("multicast_group must match the address family")
			}
		}
		if c.Interface != "" {
			if _, err = net.InterfaceByName(c.Interface); err != nil {
				return c, fmt.Errorf("network interface: %w", err)
			}
		}
		if c.Broadcast && c.Family != "udp4" {
			return c, fmt.Errorf("broadcast requires udp4")
		}
	case "websocket":
		if c.Mode == "" {
			c.Mode = "client"
		}
		if c.Mode == "client" {
			u, e := url.Parse(c.URL)
			if e != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" {
				return c, fmt.Errorf("url must be ws:// or wss:// without user credentials or fragments")
			}
			c.TLS = u.Scheme == "wss"
		} else if c.Mode == "listener" {
			if _, _, err = net.SplitHostPort(c.Address); err != nil {
				return c, fmt.Errorf("address must be host:port")
			}
			if c.Path == "" {
				c.Path = "/ws"
			}
			if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, "?#") {
				return c, fmt.Errorf("path must be an absolute http path")
			}
		} else {
			return c, fmt.Errorf("websocket mode must be client or listener")
		}
		c.headers = make(http.Header)
		if c.HeadersJSON != "" {
			var h map[string]string
			if err = json.Unmarshal([]byte(c.HeadersJSON), &h); err != nil {
				return c, fmt.Errorf("headers_json must be a JSON object of strings")
			}
			for k, v := range h {
				if !validHeaderName(k) || strings.ContainsAny(v, "\r\n") {
					return c, fmt.Errorf("invalid websocket header")
				}
				lower := strings.ToLower(k)
				if lower == "host" || lower == "connection" || lower == "upgrade" || strings.HasPrefix(lower, "sec-websocket-") {
					return c, fmt.Errorf("websocket handshake headers are managed by the driver")
				}
				c.headers.Set(k, v)
			}
		}
		for _, s := range c.Subprotocols {
			if !validHeaderName(s) {
				return c, fmt.Errorf("invalid websocket subprotocol")
			}
		}
		for _, s := range c.Origins {
			if s == "" || s == "*" {
				return c, fmt.Errorf("allowed origins must name specific hosts")
			}
		}
	default:
		return c, fmt.Errorf("unknown socket kind %q", kind)
	}
	if kind != "udp" {
		if c.tlsConfig, err = c.makeTLS(); err != nil {
			return c, err
		}
	}
	return c, nil
}

func duration(s string, fallback time.Duration) (time.Duration, error) {
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > 120*time.Second {
		return 0, fmt.Errorf("duration must be positive and at most 120s")
	}
	return d, nil
}

func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

func (c Config) makeTLS() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if (c.Certificate == "") != (c.PrivateKey == "") {
		return nil, fmt.Errorf("certificate and private key must be supplied together")
	}
	if c.Certificate != "" {
		pair, e := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey))
		if e != nil {
			return nil, fmt.Errorf("invalid tls certificate or key")
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if c.CA != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(c.CA)) {
			return nil, fmt.Errorf("ca must contain pem certificates")
		}
		cfg.RootCAs = roots
	}
	if c.TLS && c.Mode == "listener" && len(cfg.Certificates) == 0 {
		return nil, fmt.Errorf("tls listener requires a certificate and private key")
	}
	return cfg, nil
}

// KindDefs returns the settings schemas for the three protocols.
func KindDefs() []integrations.ConnectionKind {
	out := make([]integrations.ConnectionKind, 0, 3)
	for _, kind := range []string{"tcp", "udp", "websocket"} {
		k := kind
		spec := []flow.FieldSpec{}
		if k != "udp" {
			modes := []flow.Option{{Value: "client", Label: "Client"}, {Value: "listener", Label: "Listener"}}
			if k == "tcp" {
				modes = append(modes, flow.Option{Value: "request", Label: "Request only"})
			}
			spec = append(spec, flow.FieldSpec{ID: "mode", Label: "Mode", Type: flow.FieldSelect, Default: "client", Options: modes})
		}
		addressField := flow.FieldSpec{ID: "address", Label: "Address", Type: flow.FieldString, Placeholder: "127.0.0.1:9000", Help: "Bind address for listeners and UDP; remote endpoint for TCP clients."}
		if k == "websocket" {
			addressField.ShowIf = &flow.ShowIf{Field: "mode", Equals: "listener"}
		}
		spec = append(spec, addressField)
		if k == "tcp" {
			spec = append(spec,
				selectField("framing", "Incoming framing", "delimiter", "chunks", "delimiter", "fixed", "close"),
				flow.FieldSpec{ID: "delimiter", Label: "Delimiter", Type: flow.FieldString, Default: `\n`, ShowIf: &flow.ShowIf{Field: "framing", Equals: "delimiter"}, Help: "Escaped bytes, such as \\n or \\r\\n."},
				flow.FieldSpec{ID: "include_delimiter", Label: "Include delimiter", Type: flow.FieldBoolean, ShowIf: &flow.ShowIf{Field: "framing", Equals: "delimiter"}},
				flow.FieldSpec{ID: "fixed_length", Label: "Frame length", Type: flow.FieldNumber, Default: 1, ShowIf: &flow.ShowIf{Field: "framing", Equals: "fixed"}})
		}
		if k == "udp" {
			spec = append(spec, selectField("family", "Address family", "udp4", "udp4", "udp6"),
				flow.FieldSpec{ID: "destination", Label: "Default destination", Type: flow.FieldString, Placeholder: "127.0.0.1:9001"},
				flow.FieldSpec{ID: "multicast_group", Label: "Multicast group", Type: flow.FieldString},
				flow.FieldSpec{ID: "interface", Label: "Network interface", Type: flow.FieldString},
				flow.FieldSpec{ID: "ttl", Label: "Multicast TTL / hop limit", Type: flow.FieldNumber, Default: 1},
				flow.FieldSpec{ID: "loopback", Label: "Multicast loopback", Type: flow.FieldBoolean, Default: true},
				flow.FieldSpec{ID: "broadcast", Label: "Allow IPv4 broadcast", Type: flow.FieldBoolean})
		}
		if k == "websocket" {
			spec = append(spec,
				flow.FieldSpec{ID: "url", Label: "URL", Type: flow.FieldString, ShowIf: &flow.ShowIf{Field: "mode", Equals: "client"}, Placeholder: "ws://127.0.0.1:9000/ws"},
				flow.FieldSpec{ID: "headers_json", Label: "Client headers (JSON)", Type: flow.FieldString, Control: "password", ShowIf: &flow.ShowIf{Field: "mode", Equals: "client"}, Help: "JSON object of header names to string values. Stored encrypted."},
				flow.FieldSpec{ID: "path", Label: "Listener path", Type: flow.FieldString, Default: "/ws", ShowIf: &flow.ShowIf{Field: "mode", Equals: "listener"}},
				flow.FieldSpec{ID: "token", Label: "Listener bearer token", Type: flow.FieldString, Control: "password", ShowIf: &flow.ShowIf{Field: "mode", Equals: "listener"}},
				flow.FieldSpec{ID: "origins", Label: "Allowed origin hosts", Type: flow.FieldJSON, Control: "list", ShowIf: &flow.ShowIf{Field: "mode", Equals: "listener"}, Help: "Empty uses same-origin checks. List host patterns for permitted browser clients."},
				flow.FieldSpec{ID: "subprotocols", Label: "Subprotocols", Type: flow.FieldJSON, Control: "list"})
		}
		limit := 65536
		if k == "udp" {
			limit = 65507
		}
		spec = append(spec, flow.FieldSpec{ID: "max_bytes", Label: "Maximum frame bytes", Type: flow.FieldNumber, Default: limit}, flow.FieldSpec{ID: "write_timeout", Label: "Write timeout", Type: flow.FieldDuration, Default: "10s"})
		if k != "udp" {
			spec = append(spec, flow.FieldSpec{ID: "max_sessions", Label: "Maximum sessions", Type: flow.FieldNumber, Default: 16}, flow.FieldSpec{ID: "dial_timeout", Label: "Connect timeout", Type: flow.FieldDuration, Default: "10s"}, flow.FieldSpec{ID: "reconnect_delay", Label: "Reconnect delay", Type: flow.FieldDuration, Default: "1s"},
				flow.FieldSpec{ID: "tls", Label: "Listener / TCP TLS", Type: flow.FieldBoolean, Help: "WebSocket clients use the URL scheme to select TLS."},
				flow.FieldSpec{ID: "ca", Label: "Trusted CA (PEM)", Type: flow.FieldText}, flow.FieldSpec{ID: "server_name", Label: "TLS server name", Type: flow.FieldString},
				flow.FieldSpec{ID: "certificate", Label: "Certificate (PEM)", Type: flow.FieldText}, flow.FieldSpec{ID: "private_key", Label: "Private key (PEM)", Type: flow.FieldString, Control: "password"})
		}
		out = append(out, integrations.ConnectionKind{Kind: k, Label: map[string]string{"tcp": "TCP", "udp": "UDP", "websocket": "WebSocket"}[k], ConfigSpec: spec, Secrets: []string{"private_key", "headers_json", "token"}, Validate: func(raw []byte) error { _, err := ParseConfig(k, raw); return err }, Factory: New})
	}
	return out
}

func selectField(id, label, def string, values ...string) flow.FieldSpec {
	f := flow.FieldSpec{ID: id, Label: label, Type: flow.FieldSelect, Default: def}
	for _, v := range values {
		f.Options = append(f.Options, flow.Option{Value: v, Label: map[string]string{"chunks": "Chunks", "delimiter": "Delimiter", "fixed": "Fixed length", "close": "Connection closes", "udp4": "IPv4", "udp6": "IPv6"}[v]})
	}
	return f
}

// Delimiter decodes escaped bytes without reading any files or expressions.
func Delimiter(s string) ([]byte, error) {
	v, e := strconv.Unquote(`"` + s + `"`)
	if e != nil || len(v) == 0 || len(v) > 256 {
		return nil, fmt.Errorf("delimiter must encode 1 to 256 bytes")
	}
	return []byte(v), nil
}
