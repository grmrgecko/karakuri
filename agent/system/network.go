package system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

const (
	nodeNetworkProbe = "network-probe"
	probeTCP         = "tcp"
	probeHTTP        = "http"
)

// networkProbeNode checks reachability from the agent host, which can differ
// from the server across VLANs, VPNs, and split DNS.
type networkProbeNode struct{ opts agentnode.Options }

type unreachableError struct{ error }

func (networkProbeNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type: nodeNetworkProbe, Folder: folder, Label: "Network probe",
		Description: "Checks a TCP service or HTTP endpoint from this agent host",
		Icon:        "las la-network-wired", Pure: true, Retryable: true, TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []nodeapi.FieldSpec{
			{ID: "kind", Label: "Probe", Type: flow.FieldSelect, Default: probeTCP, Options: []nodeapi.Option{
				{Value: probeTCP, Label: "TCP connection"}, {Value: probeHTTP, Label: "HTTP request"},
			}},
			{ID: "address", Label: "Host and port", Type: flow.FieldString, Required: true,
				ShowIf: &nodeapi.ShowIf{Field: "kind", Equals: probeTCP}, Placeholder: "nas.local:443"},
			{ID: "url", Label: "URL", Type: flow.FieldString, Required: true,
				ShowIf: &nodeapi.ShowIf{Field: "kind", Equals: probeHTTP}, Placeholder: "https://nas.local/health"},
			{ID: "timeout", Label: "Timeout", Type: flow.FieldDuration, Default: "5s", Placeholder: "5s"},
			{ID: "fail_if_unreachable", Label: "Fail when unreachable or unhealthy", Type: flow.FieldBoolean, Default: false,
				Help: "Off reports reachable and healthy flags so the result can be used in a switch. Invalid settings and incomplete body checks still fail."},
		},
	}
	for _, f := range httpFields(true) {
		f.ShowIf = &nodeapi.ShowIf{Field: "kind", Equals: probeHTTP}
		m.ConfigSpec = append(m.ConfigSpec, f)
	}
	m.ConfigSpec = append(m.ConfigSpec, nodeapi.FieldSpec{ID: "body_contains", Label: "Body contains", Type: flow.FieldString, ShowIf: &nodeapi.ShowIf{Field: "kind", Equals: probeHTTP}, Help: "Optional case-sensitive substring; requires GET and a complete body within the agent output limit."})
	return m
}

type networkProbeConfig struct {
	httpPolicy
	BodyContains      string `json:"body_contains"`
	Kind              string `json:"kind"`
	Address           string `json:"address"`
	URL               string `json:"url"`
	Timeout           string `json:"timeout"`
	FailIfUnreachable bool   `json:"fail_if_unreachable"`
}

func (n networkProbeNode) Execute(ctx context.Context, ec nodeapi.ExecContext, _ map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg networkProbeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	timeout := 5 * time.Second
	var err error
	if raw := strings.TrimSpace(cfg.Timeout); raw != "" {
		timeout, err = time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return nil, fmt.Errorf("invalid probe timeout %q", cfg.Timeout)
		}
	}
	started := time.Now()
	if err := validateNetworkProbe(cfg); err != nil {
		return nil, err
	}
	result, err := probeNetwork(ctx, cfg, timeout, agentnode.ReadLimit(n.opts.MaxOutput))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var unreachable unreachableError
		if !errors.As(err, &unreachable) {
			return nil, err
		}
		if cfg.FailIfUnreachable {
			return nil, err
		}
		result = map[string]any{"kind": cfg.Kind, "reachable": false, "healthy": false, "error": err.Error()}
	}
	if cfg.FailIfUnreachable && result["healthy"] == false {
		return nil, fmt.Errorf("network probe did not satisfy configured health checks")
	}
	result["duration_ms"] = float64(time.Since(started).Microseconds()) / 1000
	return nodeapi.One("out", &nodeapi.Msg{Payload: result, Meta: map[string]any{"source": "network-probe"}}), nil
}

func validateNetworkProbe(cfg networkProbeConfig) error {
	switch cfg.Kind {
	case "", probeTCP:
		if _, _, err := net.SplitHostPort(strings.TrimSpace(cfg.Address)); err != nil {
			return fmt.Errorf("invalid host and port %q: %w", cfg.Address, err)
		}
	case probeHTTP:
		if err := validateHTTPHeaders(cfg.Headers); err != nil {
			return err
		}
		if _, err := parseHTTPURL(strings.TrimSpace(cfg.URL)); err != nil {
			return err
		}
		method, err := requestMethod(cfg.Method, true)
		if err != nil {
			return err
		}
		if method == "HEAD" && cfg.BodyContains != "" {
			return fmt.Errorf("body health check requires GET")
		}
		if _, err := parseStatuses(cfg.ExpectedStatuses); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown probe kind %q", cfg.Kind)
	}
	return nil
}

func probeNetwork(ctx context.Context, cfg networkProbeConfig, timeout time.Duration, limit int) (map[string]any, error) {
	switch cfg.Kind {
	case "", probeTCP:
		address := strings.TrimSpace(cfg.Address)
		conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, unreachableError{fmt.Errorf("connecting to %s: %w", address, err)}
		}
		remote := conn.RemoteAddr().String()
		conn.Close()
		return map[string]any{"kind": probeTCP, "reachable": true, "healthy": true, "address": address, "remote_address": remote}, nil
	case probeHTTP:
		target := strings.TrimSpace(cfg.URL)
		method, _ := requestMethod(cfg.Method, true)
		cfg.Method = method
		resp, closeResponse, err := doAgentHTTP(ctx, target, cfg.httpPolicy, nil, timeout)
		if err != nil {
			return nil, unreachableError{err}
		}
		defer closeResponse()
		ranges, _ := parseStatuses(cfg.ExpectedStatuses)
		statusMatches := statusAccepted(resp.StatusCode, ranges)
		bodyMatches := true
		if cfg.BodyContains != "" {
			data, err := readHTTPBody(resp.Body, limit)
			if err != nil {
				return nil, err
			}
			bodyMatches = strings.Contains(string(data), cfg.BodyContains)
		}
		return map[string]any{"kind": probeHTTP, "reachable": true, "healthy": statusMatches && bodyMatches, "status": resp.StatusCode, "status_matches": statusMatches, "body_matches": bodyMatches}, nil
	default:
		return nil, fmt.Errorf("unknown probe kind %q", cfg.Kind)
	}
}
