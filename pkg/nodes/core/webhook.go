package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// WebhookNode is the HTTP webhook trigger: while its flow is deployed and
// enabled, requests to /hook/<token> start a run. The route always responds
// 202 immediately; the run executes asynchronously. Test runs need no
// test_params: the node synthesizes a request-shaped message on demand, and a
// configured test body only replaces the payload inside it.
type WebhookNode struct{}

func (WebhookNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "webhook",
		Label:             "Webhook",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderHTTP,
		Description:       "Starts a run for every HTTP request to /hook/<token>",
		Icon:              "las la-globe",
		TestWithoutParams: true,
		Inputs: []engine.PortSpec{{
			ID: "response", Label: "Response", Type: flow.PortAny, Kind: flow.PortKindReturn,
		}},
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject,
			// The declared format decides what a request delivers. A body that
			// does not match it is answered 400 rather than passed along as
			// something else, so the port is a promise and not a guess.
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{"body_format": {webhookBodyText}}, Type: flow.PortString},
					{Match: map[string][]string{"body_format": {webhookBodyBinary}}, Type: flow.PortBinary},
					{Match: map[string][]string{"body_format": {webhookBodyQuery}}, Type: flow.PortObject},
				},
				Default: flow.PortObject,
			},
		}},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "token",
				Label:       "Token",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "my-secret-token",
				Help:        "The webhook fires on requests to /hook/<token>. The route is unauthenticated, so use a long random value. When two enabled flows share a token, the last one armed wins.",
			},
			{
				ID:      "methods",
				Label:   "Methods",
				Type:    flow.FieldString,
				Control: "tokens",
				Default: "POST",
				Options: []engine.Option{
					{Value: "POST", Label: "POST"},
					{Value: "GET", Label: "GET"},
					{Value: "PUT", Label: "PUT"},
					{Value: "PATCH", Label: "PATCH"},
					{Value: "DELETE", Label: "DELETE"},
					{Value: "HEAD", Label: "HEAD"},
					{Value: "ANY", Label: "Any method"},
				},
				Help: "HTTP methods that may fire the webhook; others get 405. Pick as many as apply. " +
					"The method is on meta.method, so a switch can route on it.",
			},
			{
				ID:          "allowed_ips",
				Label:       "Allowed callers",
				Type:        flow.FieldString,
				Control:     "tokens",
				Placeholder: "10.0.0.0/8",
				Help: "Addresses or CIDR blocks that may fire this webhook. Empty accepts anyone. " +
					"Checked against the peer address, never a forwarded-for header, since the " +
					"caller writes those; behind a reverse proxy, filter at the proxy.",
			},
			{
				ID:      "verify",
				Label:   "Verify a signature",
				Type:    flow.FieldSelect,
				Default: "none",
				Options: []engine.Option{
					{Value: "none", Label: "No"},
					{Value: "hmac", Label: "HMAC over the raw body"},
				},
				Help: "The token in the URL is a name, not a credential: anything that learns it can " +
					"fire the flow. A signature is what makes a public webhook mean something.",
			},
			{
				ID:          "signature_header",
				Label:       "Signature header",
				Type:        flow.FieldString,
				Required:    true,
				Default:     "X-Hub-Signature-256",
				Placeholder: "X-Hub-Signature-256",
				ShowIf:      &flow.ShowIf{Field: "verify", Equals: "hmac"},
			},
			{
				ID:       "signature_secret",
				Label:    "Shared secret",
				Type:     flow.FieldString,
				Control:  "password",
				Required: true,
				ShowIf:   &flow.ShowIf{Field: "verify", Equals: "hmac"},
				Help:     "The same secret configured at the sender.",
			},
			{
				ID:      "signature_algorithm",
				Label:   "Algorithm",
				Type:    flow.FieldSelect,
				Default: "sha256",
				Options: []engine.Option{
					{Value: "sha256", Label: "SHA-256"},
					{Value: "sha1", Label: "SHA-1"},
					{Value: "sha512", Label: "SHA-512"},
				},
				ShowIf: &flow.ShowIf{Field: "verify", Equals: "hmac"},
			},
			{
				ID:          "signature_prefix",
				Label:       "Signature prefix",
				Type:        flow.FieldString,
				Default:     "sha256=",
				Placeholder: "sha256=",
				ShowIf:      &flow.ShowIf{Field: "verify", Equals: "hmac"},
				Help:        "Stripped before comparing. GitHub sends sha256=, most senders send nothing.",
			},
			{
				ID:      "body_format",
				Label:   "Read the body as",
				Type:    flow.FieldSelect,
				Default: webhookBodyJSON,
				Options: []engine.Option{
					{Value: webhookBodyJSON, Label: "JSON"},
					{Value: webhookBodyText, Label: "Text"},
					{Value: webhookBodyBinary, Label: "Binary"},
					{Value: webhookBodyQuery, Label: "Query parameters"},
				},
				Help: "What the payload is read as. A request whose body does not match gets 400, " +
					"so the output port states what a run will actually carry. Query parameters is " +
					"for GET webhooks with no body. The raw body is always in meta.body.",
			},
			{
				ID:      "response_mode",
				Label:   "Response mode",
				Type:    flow.FieldSelect,
				Default: "async",
				Options: []engine.Option{
					{Value: "async", Label: "Immediate 202 Accepted"},
					{Value: "sync", Label: "Wait for the flow's response"},
				},
				Help: "Synchronous mode holds the request open until a branch returns a message to this node. A flow that never returns replies when the run ends, using the response_code, response_body, and response_headers run variables.",
			},
			{
				ID:          "response_timeout",
				Label:       "Response timeout (seconds)",
				Type:        flow.FieldNumber,
				Default:     30,
				ShowIf:      &flow.ShowIf{Field: "response_mode", Equals: "sync"},
				Placeholder: "30",
				Help:        "How long the request waits for the flow before giving up with 504.",
			},
			{
				ID: "response_status", Label: "Response status code", Type: flow.FieldNumber, Default: 200,
				ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "sync"}, Placeholder: "200",
				Help: "Overridden by the response_code run variable when a script sets one.",
			},
			{
				ID: "response_body_source", Label: "Response body", Type: flow.FieldSelect, Default: "payload",
				Options: []engine.Option{{Value: "payload", Label: "Returned payload"}, {Value: "template", Label: "Template"}},
				ShowIf:  &flow.ShowIf{Field: "response_mode", Equals: "sync"},
				Help:    "Send the returned payload, or render a template. The response_body run variable wins over both.",
			},
			{
				ID: "response_body", Label: "Response body template", Type: flow.FieldText,
				ShowIf:      &flow.ShowIf{Field: "response_body_source", Equals: "template"},
				Placeholder: "{\"ok\": true, \"value\": {{ payload.value }}}",
				Help:        "Text with {{ }} expression segments.",
			},
			{
				ID: "response_content_type", Label: "Response content type", Type: flow.FieldString,
				Default: "application/json", ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "sync"},
			},
			{
				ID: "response_headers", Label: "Response headers", Type: flow.FieldJSON,
				ShowIf: &flow.ShowIf{Field: "response_mode", Equals: "sync"},
				Help:   "Object of header name to value, for example {\"Cache-Control\": \"no-store\"}.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Body the Test Run button seeds this trigger with, read as the format above, " +
					"instead of waiting for a real request. Binary is written as base64. The rest " +
					"of the request is stood in for either way, so meta.method and meta.query still " +
					"resolve. No HTTP request is made.",
			},
		},
	}
}

// splitTokens reads a comma-separated tokens field into a list, dropping the
// blanks a trailing comma leaves behind.
func splitTokens(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// withSignature copies the signature settings onto a registration when the node
// is configured to verify one. It is a separate step so the fields are set in
// exactly one place: a registration carrying a header name but no secret would
// look like it verifies and quietly not.
func withSignature(r engine.WebhookRegistration, cfg webhookConfig) engine.WebhookRegistration {
	if cfg.Verify != "hmac" {
		return r
	}
	r.SignatureHeader = strings.TrimSpace(cfg.SigHeader)
	r.SignatureSecret = cfg.SigSecret
	r.SignatureAlgorithm = cfg.SigAlgorithm
	r.SignaturePrefix = cfg.SigPrefix
	return r
}

// Webhook body formats: how a request body becomes the trigger payload. There
// is no automatic mode. Deciding by content meant the output port could not say
// what a run would carry, which is the one thing it is there to say.
const (
	webhookBodyJSON   = "json"
	webhookBodyText   = "text"
	webhookBodyBinary = "binary"
	// webhookBodyQuery reads the query parameters instead of the body, for GET
	// webhooks that carry everything in the URL.
	webhookBodyQuery = "query"
)

// webhookConfig is the decoded webhook node configuration.
type webhookConfig struct {
	Token               string       `json:"token"`
	Methods             string       `json:"methods"`
	AllowedIPs          string       `json:"allowed_ips"`
	Verify              string       `json:"verify"`
	SigHeader           string       `json:"signature_header"`
	SigSecret           string       `json:"signature_secret"`
	SigAlgorithm        string       `json:"signature_algorithm"`
	SigPrefix           string       `json:"signature_prefix"`
	BodyFormat          string       `json:"body_format"`
	ResponseMode        string       `json:"response_mode"`
	ResponseTimeout     int          `json:"response_timeout"`
	ResponseStatus      int          `json:"response_status"`
	ResponseBodySource  string       `json:"response_body_source"`
	ResponseBody        string       `json:"response_body"`
	ResponseContentType string       `json:"response_content_type"`
	ResponseHeaders     httpFieldMap `json:"response_headers"`
}

// Execute passes the run's initial message through: runs fired by the HTTP
// route carry the request body. A test run gets a synthesized request message
// so downstream nodes see the deployed shape, carrying the configured test
// body when the node has one and an empty body of the declared format when it
// does not.
func (WebhookNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg webhookConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if inputs["response"] != nil {
		return executeWebhookResponse(ec, cfg, inputs["response"])
	}
	msg := inputs[engine.TriggerInput]
	// A seed the test-run API built is the only one missing the request
	// detail; a real delivery, and a rerun replaying one, already carry it.
	test := msg != nil && msg.Meta["source"] == "test"
	if msg != nil && msg.Payload != nil && !test {
		return engine.One("out", msg), nil
	}

	out := webhookTestMsg(cfg)

	if msg != nil {
		out.ID = msg.ID
		for k, v := range msg.Meta {
			out.Meta[k] = v
		}
		// The test body stands in for the request body, so meta.body reports
		// it the way a real request would.
		if msg.Payload != nil {
			out.Payload = msg.Payload
			out.Meta["body"] = webhookTestBody(msg.Payload)
		}
	}
	return engine.One("out", out), nil
}

func executeWebhookResponse(ec engine.ExecContext, cfg webhookConfig, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	body, err := webhookResponseBody(ec, cfg, msg)
	if err != nil {
		return nil, err
	}
	status := cfg.ResponseStatus
	if status == 0 {
		status = 200
	}
	contentType := cfg.ResponseContentType
	if contentType == "" {
		contentType = "application/json"
	}
	headers := map[string]string{"Content-Type": contentType}
	// Header names are case-insensitive, so a configured "content-type" is
	// the content type above rather than a second header beside it.
	for name, value := range cfg.ResponseHeaders {
		headers[http.CanonicalHeaderKey(strings.TrimSpace(name))] = value
	}
	resp := engine.ResponseFromVars(ec.Vars(), &engine.HTTPResponse{Status: status, Headers: headers, Body: body})
	requestID := requestIDFrom(msg)
	if requestID == "" {
		ec.Debug(nil, "no HTTP request to answer; set the webhook response mode to synchronous")
		return nil, nil
	}
	if !engine.Responses().Respond(requestID, resp) {
		ec.Debug(nil, "HTTP request was already answered or timed out")
	}
	return nil, nil
}

func webhookResponseBody(ec engine.ExecContext, cfg webhookConfig, msg *engine.Msg) (string, error) {
	if cfg.ResponseBodySource == "template" {
		rendered, err := ec.Interpolate(cfg.ResponseBody, msg)
		if err != nil {
			return "", err
		}
		return rendered, nil
	}
	switch payload := msg.Payload.(type) {
	case nil:
		return "", nil
	case string:
		return payload, nil
	case []byte:
		// Bytes are the body as they stand. The JSON encoder would answer
		// with a quoted base64 string, which is not the file being returned.
		return string(payload), nil
	default:
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("payload cannot be encoded as JSON: %w", err)
		}
		return string(encoded), nil
	}
}

func requestIDFrom(msg *engine.Msg) string {
	if msg == nil || msg.Meta == nil {
		return ""
	}
	id, _ := msg.Meta[engine.RequestIDMeta].(string)
	return strings.TrimSpace(id)
}

// webhookTestBody renders a test payload the way meta.body carries a real
// request body: the raw text of it, whatever type the format produced.
func webhookTestBody(payload any) string {
	switch v := payload.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

// webhookTestMsg builds the stand-in message for a test run: an empty payload
// with the same meta keys a real request produces, so expressions and scripts
// written against meta.method or meta.query still resolve.
func webhookTestMsg(cfg webhookConfig) *engine.Msg {
	// Methods is a token list. A request carries one method, so the stand-in
	// takes the first accepted one, or POST when any method is accepted.
	method := ""
	for _, token := range strings.Split(cfg.Methods, ",") {
		token = strings.ToUpper(strings.TrimSpace(token))
		if token == "ANY" {
			method = ""
			break
		}
		if method == "" {
			method = token
		}
	}
	if method == "" {
		method = "POST"
	}
	// The stand-in payload matches the shape the configured format promises, so
	// a test run exercises what a real request would deliver.
	var payload any = map[string]any{}
	switch cfg.BodyFormat {
	case webhookBodyText:
		payload = ""
	case webhookBodyBinary:
		payload = []byte{}
	}
	return &engine.Msg{
		Payload: payload,
		Meta: map[string]any{
			"source":       "webhook",
			"method":       method,
			"path":         "/hook/" + cfg.Token,
			"query":        map[string]any{},
			"headers":      map[string]any{},
			"body":         "",
			"content_type": "",
			"remote_addr":  "",
			"user_agent":   "",
		},
	}
}

// Start implements engine.TriggerType: it registers the token on the shared
// webhook registry until the context is canceled.
func (WebhookNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg webhookConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("webhook node has no token")
	}
	if cfg.Methods == "" {
		cfg.Methods = "POST"
	}

	sync := cfg.ResponseMode == "sync"
	timeout := time.Duration(cfg.ResponseTimeout) * time.Second
	if timeout <= 0 {
		timeout = engine.DefaultWebhookResponseTimeout
	}

	registration := engine.WebhookRegistration{
		Token:           cfg.Token,
		Methods:         cfg.Methods,
		AllowedIPs:      splitTokens(cfg.AllowedIPs),
		BodyFormat:      cfg.BodyFormat,
		Synchronous:     sync,
		ResponseTimeout: timeout,
		Emit:            emit,
	}
	unregister := engine.Webhooks().Register(withSignature(registration, cfg))
	ec.Logger().Info("webhook trigger armed",
		zap.String("token", cfg.Token), zap.String("methods", cfg.Methods),
		zap.Bool("synchronous", sync))

	<-ctx.Done()
	unregister()
	return nil
}
