package system

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"golang.org/x/net/http/httpguts"
)

type httpPolicy struct {
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	ExpectedStatuses string            `json:"expected_statuses"`
	FollowRedirects  *bool             `json:"follow_redirects"`
	AllowInsecureTLS bool              `json:"allow_insecure_tls"`
}

type statusRange struct{ first, last int }

func parseStatuses(raw string) ([]statusRange, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ranges []statusRange
	for _, part := range strings.Split(raw, ",") {
		ends := strings.Split(strings.TrimSpace(part), "-")
		if len(ends) > 2 {
			return nil, fmt.Errorf("expected statuses must contain HTTP codes or ranges")
		}
		first, err := strconv.Atoi(strings.TrimSpace(ends[0]))
		if err != nil || first < 100 || first > 599 {
			return nil, fmt.Errorf("invalid expected HTTP status %q", part)
		}
		last := first
		if len(ends) == 2 {
			last, err = strconv.Atoi(strings.TrimSpace(ends[1]))
			if err != nil || last < first || last > 599 {
				return nil, fmt.Errorf("invalid expected HTTP status range %q", part)
			}
		}
		ranges = append(ranges, statusRange{first, last})
	}
	return ranges, nil
}

func statusAccepted(status int, ranges []statusRange) bool {
	if len(ranges) == 0 {
		return true
	}
	for _, r := range ranges {
		if status >= r.first && status <= r.last {
			return true
		}
	}
	return false
}

func httpFields(probe bool) []nodeapi.FieldSpec {
	methods := []string{"GET", "HEAD"}
	if !probe {
		methods = append(methods, "OPTIONS", "POST", "PUT", "PATCH", "DELETE")
	}
	var options []nodeapi.Option
	for _, method := range methods {
		options = append(options, nodeapi.Option{Value: method, Label: method})
	}
	statusDefault := ""
	if !probe {
		statusDefault = "200-299"
	}
	return []nodeapi.FieldSpec{
		{ID: "method", Label: "Method", Type: flow.FieldSelect, Default: "GET", Options: options},
		{ID: "headers", Label: "Headers", Type: flow.FieldJSON, Control: "map", Help: "String values; request nodes support {{ }} expressions. Headers are stripped when a redirect changes origin."},
		{ID: "expected_statuses", Label: "Expected HTTP statuses", Type: flow.FieldString, Default: statusDefault, Placeholder: "200-299,304", Help: "Comma-separated codes or inclusive ranges from 100 through 599. Probe: unset checks reachability only. Request: unset accepts 200-299."},
		{ID: "follow_redirects", Label: "Follow redirects", Type: flow.FieldBoolean, Default: true, Help: "At most ten redirects. Off returns the redirect response."},
		{ID: "allow_insecure_tls", Label: "Allow invalid TLS certificates", Type: flow.FieldBoolean, Default: false},
	}
}

func requestMethod(raw string, probe bool) (string, error) {
	if raw == "" {
		raw = "GET"
	}
	switch raw {
	case "GET", "HEAD":
		return raw, nil
	case "OPTIONS", "POST", "PUT", "PATCH", "DELETE":
		if !probe {
			return raw, nil
		}
	}
	return "", fmt.Errorf("unsupported HTTP method %q", raw)
}

func parseHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("http URL must name an http or https host without embedded credentials")
	}
	return parsed, nil
}

func validateHTTPHeaders(headers map[string]string) error {
	for name, value := range headers {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("invalid http header name or value")
		}
	}
	return nil
}

// doAgentHTTP uses the agent's network directly and never replays a failed request.
func doAgentHTTP(ctx context.Context, target string, p httpPolicy, body []byte, timeout time.Duration) (*http.Response, func(), error) {
	if err := validateHTTPHeaders(p.Headers); err != nil {
		return nil, nil, err
	}
	parsed, err := parseHTTPURL(target)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("creating HTTP request")
	}
	for key, value := range p.Headers {
		// The client sends the host from the request, not from its header map.
		if strings.EqualFold(key, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(key, value)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.AllowInsecureTLS}, MaxResponseHeaderBytes: 1 << 20, ResponseHeaderTimeout: timeout, DisableKeepAlives: true}
	client := &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if p.FollowRedirects != nil && !*p.FollowRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) > 10 {
			return fmt.Errorf("http redirect limit exceeded")
		}
		// Compare against the original origin because Go reconstructs headers from it.
		original := via[0].URL
		if next.URL.Scheme != original.Scheme || !strings.EqualFold(next.URL.Host, original.Host) {
			next.Header = make(http.Header)
			next.Host = ""
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if e, ok := err.(*url.Error); ok {
			err = e.Err
		}
		return nil, nil, fmt.Errorf("http request: %w", err)
	}
	cleanup := func() { response.Body.Close(); transport.CloseIdleConnections() }
	return response, cleanup, nil
}

func readHTTPBody(body io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("reading HTTP response: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("http response exceeds %d bytes", limit)
	}
	return data, nil
}

type httpRequestNode struct{ opts agentnode.Options }

func (httpRequestNode) Meta() nodeapi.NodeMeta {
	fields := []nodeapi.FieldSpec{
		{ID: "url", Label: "URL", Type: flow.FieldString, Required: true, Help: "HTTP(S) URL requested from this agent host. Supports {{ }} expressions."},
		{ID: "body_property", Label: "Request body", Type: flow.FieldString, Placeholder: "payload", Help: "Message property sent as bytes, text or JSON. Empty sends no body."},
		{ID: "body_format", Label: "Request body format", Type: flow.FieldSelect, Default: "auto", Options: []nodeapi.Option{{Value: "auto", Label: "Bytes, text or JSON"}, {Value: "json", Label: "JSON"}, {Value: "binary", Label: "Binary (base64)"}}, Help: "Binary decodes the base64 used by binary message payloads. Ignored when Request body is empty."},
		{ID: "output_format", Label: "Response format", Type: flow.FieldSelect, Default: agentnode.FormatAuto, Options: agentnode.FormatOptions()},
		{ID: "timeout", Label: "Timeout (seconds)", Type: flow.FieldNumber, Default: 30, Help: "Positive seconds, up to one day, including fractions."},
		{ID: "max_response_bytes", Label: "Maximum response bytes", Type: flow.FieldNumber, Help: "Optional positive limit no greater than the agent's max_output; unset uses that limit."},
	}
	fields = append(fields, httpFields(false)...)
	return nodeapi.NodeMeta{Type: "http-request", Label: "HTTP request", Folder: folder, Icon: "las la-globe", Description: "Sends an HTTP request from this agent host without automatic retries", Inputs: []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}, Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Response", Type: flow.PortAny, TypeRule: agentnode.FormatRule()}, flow.ErrorPort()}, ConfigSpec: fields}
}

func (n *httpRequestNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg struct {
		httpPolicy
		URL              string  `json:"url"`
		BodyProperty     string  `json:"body_property"`
		BodyFormat       string  `json:"body_format"`
		OutputFormat     string  `json:"output_format"`
		Timeout          float64 `json:"timeout"`
		MaxResponseBytes int     `json:"max_response_bytes"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	switch cfg.OutputFormat {
	case "", agentnode.FormatAuto, agentnode.FormatJSON, agentnode.FormatText, agentnode.FormatLines, agentnode.FormatBinary:
	default:
		return nil, fmt.Errorf("unknown response format %q", cfg.OutputFormat)
	}
	var err error
	cfg.Method, err = requestMethod(cfg.Method, false)
	if err != nil {
		return nil, err
	}
	statuses := cfg.ExpectedStatuses
	if strings.TrimSpace(statuses) == "" {
		statuses = "200-299"
	}
	ranges, err := parseStatuses(statuses)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	if cfg.Timeout != 0 {
		timeout, err = waitTimeout(cfg.Timeout)
		if err != nil {
			return nil, err
		}
	}
	limit := agentnode.ReadLimit(n.opts.MaxOutput)
	if cfg.MaxResponseBytes != 0 {
		if cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes > limit {
			return nil, fmt.Errorf("response limit must be between 1 and %d bytes", limit)
		}
		limit = cfg.MaxResponseBytes
	}
	msg := agentnode.PrimaryInput(inputs)
	target, err := ec.Interpolate(strings.TrimSpace(cfg.URL), msg)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP URL template")
	}
	for key, value := range cfg.Headers {
		cfg.Headers[key], err = ec.Interpolate(value, msg)
		if err != nil {
			return nil, fmt.Errorf("invalid HTTP header template for %s", key)
		}
	}
	var body []byte
	if property := strings.TrimSpace(cfg.BodyProperty); property != "" {
		value, found := agentnode.ResolveProperty(msg, property)
		if !found {
			return nil, fmt.Errorf("request body property %q is missing", property)
		}
		var contentType string
		body, contentType, err = requestBody(value, cfg.BodyFormat)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			if cfg.Headers == nil {
				cfg.Headers = map[string]string{}
			}
			if !hasHeader(cfg.Headers, "Content-Type") {
				cfg.Headers["Content-Type"] = contentType
			}
		}
	}
	started := ec.Clock().Now()
	response, closeResponse, err := doAgentHTTP(ctx, target, cfg.httpPolicy, body, timeout)
	if err != nil {
		return nil, err
	}
	defer closeResponse()
	data, err := readHTTPBody(response.Body, limit)
	if err != nil {
		return nil, err
	}
	if !statusAccepted(response.StatusCode, ranges) {
		return nil, fmt.Errorf("http response status %d did not match expected statuses", response.StatusCode)
	}
	var payload any
	if len(data) == 0 && (cfg.OutputFormat == "" || cfg.OutputFormat == "auto" || cfg.OutputFormat == "json") && (cfg.Method == "HEAD" || response.StatusCode == 204 || response.StatusCode == 205) {
		payload = nil
	} else {
		payload, err = agentnode.DecodeOutput(cfg.OutputFormat, data)
		if err != nil {
			return nil, fmt.Errorf("decoding HTTP response: %w", err)
		}
	}
	out := agentnode.PassThrough(msg, map[string]any{"status": response.StatusCode, "headers": response.Header, "duration_ms": float64(ec.Clock().Now().Sub(started).Microseconds()) / 1000})
	out.Payload = payload
	return nodeapi.One("out", out), nil
}

func requestBody(value any, format string) ([]byte, string, error) {
	switch format {
	case "", "auto":
		switch value.(type) {
		case map[string]any, []any:
			format = "json"
		default:
			return agentnode.ValueBytes(value), "", nil
		}
	case "binary":
		if data, ok := value.([]byte); ok {
			return data, "application/octet-stream", nil
		}
		text, ok := value.(string)
		if !ok {
			return nil, "", fmt.Errorf("binary request body requires bytes or base64 text")
		}
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, "", fmt.Errorf("binary request body is not valid base64")
		}
		return data, "application/octet-stream", nil
	case "json":
	default:
		return nil, "", fmt.Errorf("unknown request body format %q", format)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encoding JSON request body: %w", err)
	}
	return data, "application/json", nil
}

func hasHeader(headers map[string]string, key string) bool {
	for name := range headers {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

func waitTimeout(seconds float64) (time.Duration, error) {
	if !(seconds > 0 && seconds <= 86400) {
		return 0, fmt.Errorf("timeout must be positive and at most one day")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
