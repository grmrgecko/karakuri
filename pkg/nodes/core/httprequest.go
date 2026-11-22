package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// maxResponseBytes caps how much of a response body the node reads into a
// message. Flow messages are held in memory and persisted into run events, so
// an unbounded download would take the server down rather than fail one node.
const maxResponseBytes = 8 << 20 // 8 MiB

// HTTPRequestNode calls an HTTP endpoint and emits the response. Unlike the
// script sandbox's HTTP bridge it reaches private addresses by design: talking
// to a local device or hub is the common case in home automation, and the flow
// author is trusted. Failures route to the error branch when one is wired.
type HTTPRequestNode struct{}

func (HTTPRequestNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "http-request",
		Label:       "HTTP Request",
		Folder:      flow.FolderHTTP,
		Description: "Calls an HTTP endpoint and emits the response",
		Icon:        "las la-globe",
		Retryable:   true,
		// Safe methods retry transient failures with bounded backoff and jitter.
		DefaultRetry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 500, Backoff: 2, MaxDelayMs: 5000, Jitter: 0.2},
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{
				ID: "out", Label: "Out", Type: flow.PortObject,
				// The declared format decides what the response becomes. A body
				// that does not match it fails the node and takes the error
				// branch, so the port states what a success actually carries.
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{
						{Match: map[string][]string{"response_format": {"text"}}, Type: flow.PortString},
						{Match: map[string][]string{"response_format": {"binary"}}, Type: flow.PortBinary},
					},
					Default: flow.PortObject,
				},
			},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{ID: "retry_mode", Label: "Retry methods", Type: flow.FieldSelect, Default: "safe", Options: []engine.Option{{Value: "safe", Label: "GET, HEAD and OPTIONS"}, {Value: "all", Label: "All methods (endpoint is safe to repeat)"}, {Value: "none", Label: "Never retry"}}, Help: "Repeating a command may repeat its action. Select all methods only when the endpoint handles repeats safely."},
			{ID: "retry_statuses", Label: "Retry statuses", Type: flow.FieldString, Default: "408,429,500,502,503,504", Help: "Comma-separated HTTP error statuses. Other statuses fail without retry. Empty uses the default list."},

			{
				ID:      "method",
				Label:   "Method",
				Type:    flow.FieldSelect,
				Default: "GET",
				Options: []engine.Option{
					{Value: "GET", Label: "GET"},
					{Value: "POST", Label: "POST"},
					{Value: "PUT", Label: "PUT"},
					{Value: "PATCH", Label: "PATCH"},
					{Value: "DELETE", Label: "DELETE"},
					{Value: "HEAD", Label: "HEAD"},
					{Value: "OPTIONS", Label: "OPTIONS"},
				},
			},
			{
				ID:          "url",
				Label:       "URL",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "http://192.168.1.50/api/state",
				Help:        "Supports {{ }} expression segments, for example http://host/api/{{ payload.id }}.",
			},
			{
				ID:    "query",
				Label: "Query parameters",
				Type:  flow.FieldJSON,
				Help:  "Object of name to value appended to the URL. Values support {{ }} segments.",
			},
			{
				ID:    "headers",
				Label: "Headers",
				Type:  flow.FieldJSON,
				Help:  "Object of header name to value. Values support {{ }} segments.",
			},
			{
				ID:      "body_source",
				Label:   "Body",
				Type:    flow.FieldSelect,
				Default: "none",
				Options: []engine.Option{
					{Value: "none", Label: "None"},
					{Value: "payload", Label: "Message payload as JSON"},
					{Value: "template", Label: "Template"},
					{Value: "form", Label: "Form encoded"},
				},
				Help: "What to send as the request body. Ignored for GET and HEAD. A text payload is sent as text and a binary one as its bytes.",
			},
			{
				ID:          "body",
				Label:       "Body template",
				Type:        flow.FieldText,
				ShowIf:      &flow.ShowIf{Field: "body_source", Equals: "template"},
				Placeholder: "{\"state\": \"{{ payload.state }}\"}",
				Help:        "Text with {{ }} expression segments.",
			},
			{
				ID:     "form",
				Label:  "Form fields",
				Type:   flow.FieldJSON,
				ShowIf: &flow.ShowIf{Field: "body_source", Equals: "form"},
				Help:   "Object of field name to value, sent as application/x-www-form-urlencoded.",
			},
			{
				ID:      "content_type",
				Label:   "Content type",
				Type:    flow.FieldString,
				ShowIf:  &flow.ShowIf{Field: "body_source", Equals: "template"},
				Default: "application/json",
			},
			{
				ID:      "auth",
				Label:   "Authentication",
				Type:    flow.FieldSelect,
				Default: "none",
				Options: []engine.Option{
					{Value: "none", Label: "None"},
					{Value: "basic", Label: "Basic"},
					{Value: "bearer", Label: "Bearer token"},
					{Value: "header", Label: "API key in a header"},
					{Value: "query", Label: "API key in a query parameter"},
					{Value: "oauth2", Label: "OAuth2 connection"},
				},
			},
			{
				ID:             "auth_connection",
				Label:          "Connection",
				Type:           flow.FieldConnection,
				Required:       true,
				ConnectionKind: "oauth2",
				ShowIf:         &flow.ShowIf{Field: "auth", Equals: "oauth2"},
				Help: "OAuth2 connection whose access token is sent as a bearer. The connection " +
					"exchanges and caches the token, so every node naming it shares one credential.",
			},
			{
				ID:      "username",
				Label:   "Username",
				Type:    flow.FieldString,
				ShowIf:  &flow.ShowIf{Field: "auth", Equals: "basic"},
				Control: "password",
			},
			{
				ID:      "password",
				Label:   "Password",
				Type:    flow.FieldString,
				ShowIf:  &flow.ShowIf{Field: "auth", Equals: "basic"},
				Control: "password",
			},
			{
				ID:      "token",
				Label:   "Token",
				Type:    flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "auth", Equals: []string{"bearer", "header", "query"}},
				Help:    "The secret itself. Supports {{ }} segments.",
			},
			{
				ID:          "auth_name",
				Label:       "Sent as",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "X-API-Key",
				ShowIf:      &flow.ShowIf{Field: "auth", Equals: []string{"header", "query"}},
				Help: "Header name, or query parameter name. A key in a query string ends up in " +
					"server logs and browser history, so prefer the header where the API allows both.",
			},
			{
				ID:      "response_format",
				Label:   "Response format",
				Type:    flow.FieldSelect,
				Default: "json",
				Options: []engine.Option{
					{Value: "json", Label: "JSON"},
					{Value: "text", Label: "Text"},
					{Value: "binary", Label: "Binary"},
				},
				Help: "How the response body is read. A body that does not match fails the node, " +
					"so the error branch and retries apply. Turn binary into text with a base64 " +
					"encode node.",
			},
			{
				ID:      "fail_on_error_status",
				Label:   "Fail on 4xx and 5xx",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Treat error statuses as failures; selected statuses may retry, then use the error branch. Off emits the response either way.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout",
				Type:        flow.FieldDuration,
				Default:     "30s",
				Placeholder: "30s",
			},
			{
				ID:      "follow_redirects",
				Label:   "Follow redirects",
				Type:    flow.FieldBoolean,
				Default: true,
			},
			{
				ID:          "max_redirects",
				Label:       "Redirect limit",
				Type:        flow.FieldString,
				Default:     "5",
				Placeholder: "5",
				ShowIf:      &flow.ShowIf{Field: "follow_redirects", Equals: "true"},
				Help:        "Fail after this many hops rather than burning the timeout on a redirect loop.",
			},
			{
				ID:      "insecure_tls",
				Label:   "Skip TLS verification",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Accept any server certificate. Needed for local devices with self-signed certificates.",
			},
			{
				ID:          "proxy",
				Label:       "Proxy",
				Type:        flow.FieldString,
				Placeholder: "http://proxy.lan:3128",
				Help: "Route this request through a proxy. Empty goes direct; the environment's " +
					"HTTP_PROXY is deliberately not consulted, so what a flow does is what it says.",
			},
			{
				ID:          "client_cert",
				Label:       "Client certificate",
				Type:        flow.FieldText,
				Placeholder: "-----BEGIN CERTIFICATE-----",
				Help:        "PEM certificate presented to the server, for an endpoint that wants mutual TLS.",
			},
			{
				ID:          "client_key",
				Label:       "Client key",
				Type:        flow.FieldText,
				Control:     "password",
				Placeholder: "-----BEGIN PRIVATE KEY-----",
				Help:        "PEM private key for the certificate above.",
			},
			{
				ID:      "pagination",
				Label:   "Pagination",
				Type:    flow.FieldSelect,
				Default: paginateOff,
				Options: []engine.Option{
					{Value: paginateOff, Label: "Off, one request"},
					{Value: paginateParam, Label: "Advance a query parameter"},
					{Value: paginateNextURL, Label: "Follow a link in the response body"},
					{Value: paginateLinkHdr, Label: "Follow the Link header, rel=next"},
				},
				Help: "Keeps asking until the API runs out or the page cap is reached, then emits " +
					"the whole result. Every page is one request; the cap is what bounds it.",
			},
			{
				ID:          "page_param",
				Label:       "Page parameter",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "page",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: paginateParam},
				Help:        "Query parameter carrying the page. Set alongside a limit in the ordinary query parameters.",
			},
			{
				ID:          "page_start",
				Label:       "First page",
				Type:        flow.FieldString,
				Default:     "1",
				Placeholder: "1",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: paginateParam},
				Help:        "1 for page numbers, 0 for an offset.",
			},
			{
				ID:          "page_step",
				Label:       "Advance by",
				Type:        flow.FieldString,
				Default:     "1",
				Placeholder: "1",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: paginateParam},
				Help: "1 counts pages. Setting this to the page size counts offsets instead, which " +
					"is the other half of how APIs do this.",
			},
			{
				ID:          "next_url_path",
				Label:       "Next link",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "payload.links.next",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: paginateNextURL},
				Help: "Dot path to the link. A relative link resolves against the page it came from, " +
					"and a missing one ends the loop.",
			},
			{
				ID:      "stop_on",
				Label:   "Stop when",
				Type:    flow.FieldSelect,
				Default: "empty",
				Options: []engine.Option{
					{Value: "empty", Label: "A page comes back with no items"},
					{Value: "expression", Label: "An expression says so"},
				},
				ShowIf: &flow.ShowIf{Field: "pagination", Equals: paginateParam},
			},
			{
				ID:          "stop_expression",
				Label:       "Stop expression",
				Type:        flow.FieldExpression,
				Required:    true,
				Placeholder: "payload.has_more == false",
				ShowIf:      &flow.ShowIf{Field: "stop_on", Equals: "expression"},
				Help:        "Evaluated against each page as the message it would be. Truthy ends the loop.",
			},
			{
				ID:          "items_path",
				Label:       "Items",
				Type:        flow.FieldString,
				Placeholder: "payload.results",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: []string{paginateParam, paginateNextURL, paginateLinkHdr}},
				Help: "Dot path to the list inside a page. Empty means the payload is the list. " +
					"This is what gets concatenated, and what an empty page is judged by.",
			},
			{
				ID:          "max_pages",
				Label:       "Page limit",
				Type:        flow.FieldString,
				Default:     "10",
				Placeholder: "10",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: []string{paginateParam, paginateNextURL, paginateLinkHdr}},
				Help:        "Hard stop. The loop is driven by what the server says, so it needs one.",
			},
			{
				ID:          "page_delay",
				Label:       "Wait between pages",
				Type:        flow.FieldDuration,
				Placeholder: "200ms",
				ShowIf:      &flow.ShowIf{Field: "pagination", Equals: []string{paginateParam, paginateNextURL, paginateLinkHdr}},
				Help:        "Spaces the requests out for an API that rate limits. The wait does not count toward the run timeout.",
			},
			{
				ID:      "page_output",
				Label:   "Emit",
				Type:    flow.FieldSelect,
				Default: "items",
				Options: []engine.Option{
					{Value: "items", Label: "One message holding every page's items"},
					{Value: "pages", Label: "A message per page"},
				},
				ShowIf: &flow.ShowIf{Field: "pagination", Equals: []string{paginateParam, paginateNextURL, paginateLinkHdr}},
				Help:   "A message per page fans out like a split, so a join downstream can recombine them.",
			},
		},
	}
}

// httpRequestConfig is the decoded HTTP request node configuration.
type httpRequestConfig struct {
	RetryMode       string `json:"retry_mode"`
	RetryStatuses   string `json:"retry_statuses"`
	retryCodes      map[int]bool
	Method          string       `json:"method"`
	URL             string       `json:"url"`
	Query           httpFieldMap `json:"query"`
	Headers         httpFieldMap `json:"headers"`
	BodySource      string       `json:"body_source"`
	Body            string       `json:"body"`
	Form            httpFieldMap `json:"form"`
	ContentType     string       `json:"content_type"`
	Auth            string       `json:"auth"`
	Username        string       `json:"username"`
	Password        string       `json:"password"`
	Token           string       `json:"token"`
	AuthName        string       `json:"auth_name"`
	AuthConnection  string       `json:"auth_connection"`
	ResponseFormat  string       `json:"response_format"`
	FailOnErrStatus *bool        `json:"fail_on_error_status"`
	Timeout         string       `json:"timeout"`
	FollowRedirects *bool        `json:"follow_redirects"`
	MaxRedirects    string       `json:"max_redirects"`
	InsecureTLS     bool         `json:"insecure_tls"`
	Proxy           string       `json:"proxy"`
	ClientCert      string       `json:"client_cert"`
	ClientKey       string       `json:"client_key"`
	Pagination      string       `json:"pagination"`
	PageParam       string       `json:"page_param"`
	PageStart       string       `json:"page_start"`
	PageStep        string       `json:"page_step"`
	NextURLPath     string       `json:"next_url_path"`
	StopOn          string       `json:"stop_on"`
	StopExpression  string       `json:"stop_expression"`
	ItemsPath       string       `json:"items_path"`
	MaxPages        string       `json:"max_pages"`
	PageDelay       string       `json:"page_delay"`
	PageOutput      string       `json:"page_output"`
}

// httpFieldMap is a query, header, or form object. Its values are text on the
// wire, but the object is written as JSON, where a page size or a flag is
// naturally a number or a boolean, so those are accepted and kept as written.
type httpFieldMap map[string]string

// UnmarshalJSON reads an object of name to scalar. A nested object or array
// has no single text form and is refused by name.
func (m *httpFieldMap) UnmarshalJSON(raw []byte) error {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return errors.New("must be an object of name to value")
	}
	out := make(httpFieldMap, len(decoded))
	for name, value := range decoded {
		text := strings.TrimSpace(string(value))
		switch {
		case text == "null":
			out[name] = ""
		case strings.HasPrefix(text, `"`):
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return fmt.Errorf("value of %q is not valid text", name)
			}
			out[name] = s
		case strings.HasPrefix(text, "{"), strings.HasPrefix(text, "["):
			return fmt.Errorf("value of %q must be text, a number, or a boolean", name)
		default:
			// A number or a boolean, sent as the digits or word it was written
			// with, so 1000000 does not become 1e+06.
			out[name] = text
		}
	}
	*m = out
	return nil
}

// Execute builds the request from config and the incoming message, performs it,
// and emits the response as the outgoing payload.
func (HTTPRequestNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (outputs map[string][]*engine.Msg, err error) {
	defer func() {
		var failure *engine.ExecutionError
		if err != nil && !errors.As(err, &failure) {
			err = &engine.ExecutionError{Message: err.Error(), Cause: err, Retryable: false}
		}
	}()
	var cfg httpRequestConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	if err := cfg.validateRetry(); err != nil {
		return nil, err
	}

	timeout, err := parseDurationDefault(cfg.Timeout, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout %q: %w", cfg.Timeout, err)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	client, err := httpRequestClient(cfg, timeout)
	if err != nil {
		return nil, err
	}
	// The client is built per execution, so its idle connections would
	// otherwise sit in a transport nothing references until the peer hangs
	// up, which a Go server or a device never does.
	defer client.CloseIdleConnections()

	// One request, or a page loop. Both come back as pages so the message is
	// assembled the same way either way.
	pages, err := fetchPages(ctx, ec, cfg, msg, client)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, errors.New("the request produced no response")
	}
	if !cfg.paginating() {
		return engine.One("out", pages[0].asMessage(msg)), nil
	}
	return paginatedOutput(ec, cfg, msg, pages)
}

// fetchOnePage performs a single request and reads the response. nextURL
// overrides the configured URL when pagination is following a link; pageValue
// is written into the page parameter when pagination is advancing one.
func fetchOnePage(ctx context.Context, ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg, client *http.Client, nextURL string, pageValue int) (httpPage, error) {
	req, err := buildHTTPRequest(ctx, ec, cfg, msg, nextURL, pageValue)
	if err != nil {
		return httpPage{}, err
	}
	shown := shownURL(req.URL, cfg)
	safeURL := failureURL(req.URL)
	details := map[string]any{"method": req.Method, "url": safeURL}
	fail := func(message string, cause error, retryable bool, retryAfter int64) (httpPage, error) {
		return httpPage{}, &engine.ExecutionError{Message: message, Cause: cause, Retryable: retryable && cfg.mayRetry(req.Method), RetryAfterMS: retryAfter, Details: details}
	}
	started := ec.Clock().Now()
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fail(fmt.Sprintf("%s %s: %s", req.Method, safeURL, err), err, transientHTTPError(err), 0)
	}
	defer resp.Body.Close()
	details["status"] = resp.StatusCode
	details["headers"] = safeResponseHeaders(resp.Header)
	statusFailed := resp.StatusCode >= 400 && (cfg.FailOnErrStatus == nil || *cfg.FailOnErrStatus)
	limit := maxResponseBytes
	if statusFailed {
		limit = maxErrorBodyBytes
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	details["body"], details["body_truncated"] = boundedErrorBody(raw)
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), ec.Clock().Now())
	if err != nil {
		retryable := transientHTTPError(err)
		if statusFailed {
			retryable = retryable && cfg.retryCodes[resp.StatusCode]
		}
		return fail(fmt.Sprintf("reading response from %s: %s", safeURL, err), err, retryable, retryAfter)
	}
	if statusFailed {
		return fail(fmt.Sprintf("%s %s returned %s", req.Method, safeURL, resp.Status), nil, cfg.retryCodes[resp.StatusCode], retryAfter)
	}
	if len(raw) > maxResponseBytes {
		return fail(fmt.Sprintf("response from %s exceeds the %d byte limit", safeURL, maxResponseBytes), nil, false, 0)
	}
	payload, err := decodeHTTPBody(raw, cfg.ResponseFormat)
	if err != nil {
		return fail(fmt.Sprintf("decoding response from %s: %s", safeURL, err), err, false, 0)
	}
	return httpPage{
		payload:    payload,
		status:     resp.StatusCode,
		headers:    flattenHeaders(resp.Header),
		url:        shown,
		durationMs: ec.Clock().Now().Sub(started).Milliseconds(),
		requestURL: req.URL,
	}, nil
}

// shownURL renders a request URL for metadata and error text. Userinfo is
// masked, and so is an API key sent as a query parameter: run history and
// error messages are the server's own records, and the field help promises
// the key stays out of them.
func shownURL(u *url.URL, cfg httpRequestConfig) string {
	name := strings.TrimSpace(cfg.AuthName)
	if cfg.Auth != "query" || name == "" {
		return u.Redacted()
	}
	shown := *u
	q := shown.Query()
	if q.Has(name) {
		q.Set(name, "xxxxx")
		shown.RawQuery = q.Encode()
	}
	return shown.Redacted()
}

// buildHTTPRequest assembles the outgoing request: interpolated URL, query,
// headers, body, and authentication.
//
// nextURL replaces the configured URL when pagination is following a link,
// which is why it skips the template and the configured query: the server built
// that URL and it already carries whatever it wants carried. pageValue is
// written into the page parameter when pagination is advancing one.
func buildHTTPRequest(ctx context.Context, ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg, nextURL string, pageValue int) (*http.Request, error) {
	rawURL := strings.TrimSpace(nextURL)
	fromLink := rawURL != ""
	if !fromLink {
		var err error
		rawURL, err = ec.Interpolate(strings.TrimSpace(cfg.URL), msg)
		if err != nil {
			return nil, fmt.Errorf("invalid URL template: %w", err)
		}
	}
	if rawURL == "" {
		return nil, errors.New("http-request node has no URL")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("invalid URL: check its syntax")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("URL must use http or https")
	}

	// Merge configured query parameters over any already in the URL.
	if len(cfg.Query) > 0 && !fromLink {
		q := parsed.Query()
		for name, value := range cfg.Query {
			rendered, err := ec.Interpolate(value, msg)
			if err != nil {
				return nil, fmt.Errorf("invalid query parameter %q: %w", name, err)
			}
			q.Set(name, rendered)
		}
		parsed.RawQuery = q.Encode()
	}

	// The page parameter is set last of the query so it wins over a stale one
	// left in the configured URL.
	if cfg.Pagination == paginateParam && !fromLink {
		name := strings.TrimSpace(cfg.PageParam)
		if name == "" {
			return nil, errors.New("advancing a query parameter needs the parameter's name")
		}
		q := parsed.Query()
		q.Set(name, strconv.Itoa(pageValue))
		parsed.RawQuery = q.Encode()
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}

	body, contentType, err := httpRequestBody(ec, cfg, msg, method)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), reader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range cfg.Headers {
		rendered, err := ec.Interpolate(value, msg)
		if err != nil {
			return nil, fmt.Errorf("invalid header %q: %w", name, err)
		}
		req.Header.Set(name, rendered)
	}

	// Authentication is applied last so it wins over a hand-set header.
	switch cfg.Auth {
	case "basic":
		user, err := ec.Interpolate(cfg.Username, msg)
		if err != nil {
			return nil, fmt.Errorf("invalid username template: %w", err)
		}
		pass, err := ec.Interpolate(cfg.Password, msg)
		if err != nil {
			return nil, fmt.Errorf("invalid password template: %w", err)
		}
		req.SetBasicAuth(user, pass)
	case "bearer":
		token, err := ec.Interpolate(strings.TrimSpace(cfg.Token), msg)
		if err != nil {
			return nil, fmt.Errorf("invalid token template: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "header":
		token, name, err := httpAPIKey(ec, cfg, msg)
		if err != nil {
			return nil, err
		}
		req.Header.Set(name, token)
	case "query":
		token, name, err := httpAPIKey(ec, cfg, msg)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Set(name, token)
		req.URL.RawQuery = q.Encode()
	case "oauth2":
		token, err := oauth2Token(ctx, ec, cfg)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// oauth2TokenSource is the driver surface the oauth2 auth mode needs,
// satisfied structurally by *oauth2conn.Driver so the node does not depend on
// the driver package.
type oauth2TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

// oauth2Token resolves the configured OAuth2 connection and returns a valid
// access token. The driver caches and refreshes it, so per page and per retry
// this is usually a map lookup rather than an exchange.
func oauth2Token(ctx context.Context, ec engine.ExecContext, cfg httpRequestConfig) (string, error) {
	raw, err := ec.Connection(cfg.AuthConnection)
	if err != nil {
		return "", err
	}
	src, ok := raw.(oauth2TokenSource)
	if !ok {
		return "", fmt.Errorf("connection %s is not an OAuth2 connection", cfg.AuthConnection)
	}
	token, err := src.AccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching the OAuth2 access token: %w", err)
	}
	return token, nil
}

// httpAPIKey renders the API key and the name it is sent under, shared by the
// header and query forms.
func httpAPIKey(ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg) (string, string, error) {
	name := strings.TrimSpace(cfg.AuthName)
	if name == "" {
		return "", "", errors.New("API key authentication needs the header or parameter name")
	}
	token, err := ec.Interpolate(strings.TrimSpace(cfg.Token), msg)
	if err != nil {
		return "", "", fmt.Errorf("invalid token template: %w", err)
	}
	return token, name, nil
}

// httpRequestBody renders the request body and the content type that goes with
// it. GET and HEAD never carry one.
func httpRequestBody(ec engine.ExecContext, cfg httpRequestConfig, msg *engine.Msg, method string) (string, string, error) {
	if method == http.MethodGet || method == http.MethodHead {
		return "", "", nil
	}
	switch cfg.BodySource {
	case "", "none":
		return "", "", nil
	case "payload":
		if msg.Payload == nil {
			return "", "application/json", nil
		}
		if s, ok := msg.Payload.(string); ok {
			return s, "text/plain; charset=utf-8", nil
		}
		// Bytes are the body as they stand. The JSON encoder would send them
		// as a quoted base64 string, which is not the file being uploaded.
		if b, ok := msg.Payload.([]byte); ok {
			return string(b), "application/octet-stream", nil
		}
		encoded, err := json.Marshal(msg.Payload)
		if err != nil {
			return "", "", fmt.Errorf("payload cannot be encoded as JSON: %w", err)
		}
		return string(encoded), "application/json", nil
	case "template":
		rendered, err := ec.Interpolate(cfg.Body, msg)
		if err != nil {
			return "", "", fmt.Errorf("invalid body template: %w", err)
		}
		contentType := cfg.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		return rendered, contentType, nil
	case "form":
		form := url.Values{}
		for name, value := range cfg.Form {
			rendered, err := ec.Interpolate(value, msg)
			if err != nil {
				return "", "", fmt.Errorf("invalid form field %q: %w", name, err)
			}
			form.Set(name, rendered)
		}
		return form.Encode(), "application/x-www-form-urlencoded", nil
	default:
		return "", "", fmt.Errorf("unknown body source %q", cfg.BodySource)
	}
}

// httpRequestClient builds the client for one call. Clients are per-request
// because timeout, redirect, and TLS behavior are all node config.
func httpRequestClient(cfg httpRequestConfig, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}

	// TLS: skipping verification and presenting a client certificate are
	// independent, and an endpoint wanting mutual TLS with a private CA needs
	// both at once.
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureTLS}
	if strings.TrimSpace(cfg.ClientCert) != "" || strings.TrimSpace(cfg.ClientKey) != "" {
		pair, err := tls.X509KeyPair([]byte(cfg.ClientCert), []byte(cfg.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("client certificate and key do not load as a pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
	}
	transport.TLSClientConfig = tlsCfg

	// The proxy is taken from config only. Reading HTTP_PROXY out of the
	// environment would make one flow behave differently on two installs with
	// nothing in the graph to explain it.
	if proxy := strings.TrimSpace(cfg.Proxy); proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, errors.New("invalid proxy URL: check its syntax")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	client := &http.Client{Timeout: timeout, Transport: transport}
	if cfg.FollowRedirects != nil && !*cfg.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return client, nil
	}
	// Cap redirects so a redirect loop fails fast instead of burning the timeout.
	limit := 5
	if raw := strings.TrimSpace(cfg.MaxRedirects); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("redirect limit %q is not a whole number", raw)
		}
		limit = n
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= limit {
			return fmt.Errorf("stopped after %d redirects", limit)
		}
		return nil
	}
	return client, nil
}

// decodeHTTPBody turns the response bytes into a payload according to the
// configured format. A body that does not match the declared format is an
// error: handing back a string where the port promised an object would break
// downstream property paths, and failing routes to the error branch instead.
func decodeHTTPBody(raw []byte, format string) (any, error) {
	switch format {
	case "text":
		return string(raw), nil
	case "binary":
		return raw, nil
	default:
		return decodeJSONBody(raw)
	}
}

// decodeJSONBody parses a JSON body. An empty body is a nil payload rather than
// a parse failure: a 204 or a HEAD reply carries no document to parse.
func decodeJSONBody(raw []byte) (any, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("body is not valid JSON: %w", err)
	}
	return payload, nil
}

// flattenHeaders renders response headers for message metadata, joining
// repeated values so the result stays a flat map of strings. The map is typed
// as a decoded JSON object because that is the only shape a property path
// walks; a string-typed map would hide every header from meta.headers.<name>.
func flattenHeaders(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for name, values := range h {
		out[name] = strings.Join(values, ", ")
	}
	return out
}

// parseDurationDefault parses a duration string, falling back to def when the
// value is empty. Bare numbers are read as seconds so "30" behaves sensibly.
func parseDurationDefault(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	return time.ParseDuration(s)
}

// parseRequiredDuration parses a duration field that has no fallback. An empty
// value is an error, since reading it as zero would turn a wait into a
// passthrough.
func parseRequiredDuration(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return 0, errors.New("a duration is required")
	}
	return parseDurationDefault(s, 0)
}
