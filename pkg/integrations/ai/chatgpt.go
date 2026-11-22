package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The ChatGPT-account mode speaks the Codex backend: the endpoint Codex CLI
// uses, billed to a ChatGPT Plus/Pro subscription rather than a platform API
// key. Authentication uses the same device authorization and OAuth grant as
// Codex CLI. The connection dialog opens OpenAI sign-in, then stores the
// returned refresh credential encrypted. The client ID is Codex CLI's own,
// which is what the subscription grant is issued to.
const (
	codexClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexBaseURL             = "https://chatgpt.com/backend-api/codex"
	codexDeviceVerification  = "https://auth.openai.com/codex/device"
	codexDeviceAuthCallback  = "https://auth.openai.com/deviceauth/callback"
	defaultDevicePollSeconds = 5
)

// codexTokenURL is a variable so the refresh flow is testable against a local
// fixture; production never changes it.
var (
	codexTokenURL       = "https://auth.openai.com/oauth/token"
	codexDeviceCodeURL  = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL = "https://auth.openai.com/api/accounts/deviceauth/token"
)

// CodexDeviceLogin identifies a pending OpenAI device authorization.
type CodexDeviceLogin struct {
	DeviceAuthID    string `json:"device_auth_id"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
}

// CodexDeviceLoginResult reports whether authorization is still waiting and
// carries the credential document once OpenAI approves it.
type CodexDeviceLoginResult struct {
	Pending     bool   `json:"pending"`
	Credentials string `json:"credentials,omitempty"`
}

// StartCodexDeviceLogin asks OpenAI for the short code shown in the browser.
func StartCodexDeviceLogin(ctx context.Context) (CodexDeviceLogin, error) {
	var response struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
	}
	if err := doJSON(ctx, http.MethodPost, codexDeviceCodeURL, nil,
		map[string]string{"client_id": codexClientID}, &response); err != nil {
		return CodexDeviceLogin{}, fmt.Errorf("starting Codex login: %w", err)
	}
	if response.DeviceAuthID == "" || response.UserCode == "" {
		return CodexDeviceLogin{}, fmt.Errorf("starting Codex login: OpenAI returned no device code")
	}
	interval := defaultDevicePollSeconds
	switch value := response.Interval.(type) {
	case float64:
		interval = int(value)
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			interval = parsed
		}
	}
	if interval < defaultDevicePollSeconds {
		interval = defaultDevicePollSeconds
	}
	return CodexDeviceLogin{
		DeviceAuthID: response.DeviceAuthID, UserCode: response.UserCode,
		VerificationURL: codexDeviceVerification, Interval: interval,
	}, nil
}

// PollCodexDeviceLogin checks one pending authorization and exchanges the
// approved code for the refresh token stored on the connection.
func PollCodexDeviceLogin(ctx context.Context, deviceAuthID, userCode string) (CodexDeviceLoginResult, error) {
	deviceAuthID = strings.TrimSpace(deviceAuthID)
	userCode = strings.TrimSpace(userCode)
	if deviceAuthID == "" || userCode == "" {
		return CodexDeviceLoginResult{}, fmt.Errorf("polling Codex login: device authorization is missing")
	}

	var approval struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	status, err := postCodexDeviceJSON(ctx, codexDeviceTokenURL,
		map[string]string{"device_auth_id": deviceAuthID, "user_code": userCode}, &approval)
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return CodexDeviceLoginResult{Pending: true}, nil
	}
	if err != nil {
		return CodexDeviceLoginResult{}, fmt.Errorf("polling Codex login: %w", err)
	}
	if status < 200 || status > 299 {
		return CodexDeviceLoginResult{}, fmt.Errorf("polling Codex login: OpenAI returned %s", http.StatusText(status))
	}
	if approval.AuthorizationCode == "" || approval.CodeVerifier == "" {
		return CodexDeviceLoginResult{}, fmt.Errorf("polling Codex login: OpenAI returned an incomplete authorization")
	}

	var auth codexAuth
	status, err = postCodexForm(ctx, codexTokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {approval.AuthorizationCode},
		"redirect_uri": {codexDeviceAuthCallback}, "client_id": {codexClientID},
		"code_verifier": {approval.CodeVerifier},
	}, &auth)
	if err != nil {
		return CodexDeviceLoginResult{}, fmt.Errorf("finishing Codex login: %w", err)
	}
	if status < 200 || status > 299 {
		return CodexDeviceLoginResult{}, fmt.Errorf("finishing Codex login: OpenAI returned %s", http.StatusText(status))
	}
	if auth.RefreshToken == "" {
		return CodexDeviceLoginResult{}, fmt.Errorf("finishing Codex login: OpenAI returned no refresh token")
	}
	if auth.AccountID == "" {
		auth.AccountID = chatGPTAccountID(auth.IDToken)
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return CodexDeviceLoginResult{}, fmt.Errorf("encoding Codex credentials: %w", err)
	}
	return CodexDeviceLoginResult{Credentials: string(raw)}, nil
}

func postCodexDeviceJSON(ctx context.Context, endpoint string, body, out any) (int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return doCodexAuthRequest(req, out)
}

func postCodexForm(ctx context.Context, endpoint string, values url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doCodexAuthRequest(req, out)
}

func doCodexAuthRequest(req *http.Request, out any) (int, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(raw) > maxResponseBytes {
		return resp.StatusCode, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding response: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if message := apiErrorMessage(raw); message != "" {
			return resp.StatusCode, fmt.Errorf("%s: %s", resp.Status, message)
		}
	}
	return resp.StatusCode, nil
}

// codexModels is the catalog the ChatGPT mode offers. The Codex backend
// serves no model listing, so the catalog is the models the subscription
// grant reaches.
var codexModels = []string{"gpt-5", "gpt-5-codex", "gpt-5-mini", "gpt-5-nano", "codex-mini-latest"}

// refreshMargin renews the access token this long before it expires, so a
// request never rides a token that dies mid-flight.
const refreshMargin = time.Minute

// codexAuth is the credential set parsed from Codex CLI's auth.json.
type codexAuth struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// parseCodexAuth reads a pasted auth.json — the CLI's {"tokens": {...}}
// envelope or the bare token object. The refresh token is the durable
// credential; the account ID falls back to the id_token's claim when the
// file does not carry it.
func parseCodexAuth(raw string) (codexAuth, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return codexAuth{}, fmt.Errorf("no Codex credentials; sign in with ChatGPT")
	}
	var envelope struct {
		Tokens *codexAuth `json:"tokens"`
	}
	var auth codexAuth
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return codexAuth{}, fmt.Errorf("credentials are not valid JSON: %w", err)
	}
	if envelope.Tokens != nil {
		auth = *envelope.Tokens
	} else if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return codexAuth{}, fmt.Errorf("credentials are not valid JSON: %w", err)
	}
	if auth.RefreshToken == "" {
		return codexAuth{}, fmt.Errorf("credentials hold no refresh_token; sign in with ChatGPT again")
	}
	if auth.AccountID == "" {
		auth.AccountID = chatGPTAccountID(auth.IDToken)
	}
	return auth, nil
}

// chatGPTAccountID digs the ChatGPT account ID out of an id_token's
// https://api.openai.com/auth claim, where OpenAI puts it.
func chatGPTAccountID(idToken string) string {
	claims := jwtClaims(idToken)
	if claims == nil {
		return ""
	}
	authClaim, _ := claims["https://api.openai.com/auth"].(map[string]any)
	id, _ := authClaim["chatgpt_account_id"].(string)
	return id
}

// jwtClaims decodes a JWT's payload without verifying it — the claims are
// read for routing, never trusted for authorization; the backend verifies.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

// jwtExpiry reads a JWT's exp claim; zero when the token has none readable.
func jwtExpiry(token string) time.Time {
	claims := jwtClaims(token)
	exp, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(exp), 0)
}

// codexWire speaks the Codex backend's Responses API. Tokens refresh in
// memory: the stored refresh token is the anchor, and a rotated one replaces
// it for the process's life. After a long shutdown a revoked refresh token
// means signing in again.
type codexWire struct {
	base    string
	session string
	persist func(map[string]string) error

	mu   sync.Mutex
	auth codexAuth
	exp  time.Time
}

// newCodexWire builds the ChatGPT-account wire from a validated config.
func newCodexWire(cfg Config, persistSecrets func(map[string]string) error) (wire, error) {
	auth, err := parseCodexAuth(cfg.ChatGPTAuth)
	if err != nil {
		return nil, err
	}
	base := cfg.BaseURL
	if base == "" || base == "https://api.openai.com/v1" {
		base = codexBaseURL
	}
	return &codexWire{
		base:    strings.TrimRight(base, "/"),
		session: uuid.NewString(),
		persist: persistSecrets,
		auth:    auth,
		exp:     jwtExpiry(auth.AccessToken),
	}, nil
}

// token returns a live access token and the account ID, refreshing when the
// current token is missing, expired, or inside the margin.
func (w *codexWire) token(ctx context.Context) (string, string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.auth.AccessToken != "" && (w.exp.IsZero() || time.Until(w.exp) > refreshMargin) {
		return w.auth.AccessToken, w.auth.AccountID, nil
	}

	var resp struct {
		IDToken      string  `json:"id_token"`
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
	}
	err := doJSON(ctx, "POST", codexTokenURL, nil, map[string]string{
		"client_id":     codexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": w.auth.RefreshToken,
		"scope":         "openid profile email",
	}, &resp)
	if err != nil {
		return "", "", fmt.Errorf("refreshing ChatGPT tokens: %w", err)
	}
	if resp.AccessToken == "" {
		return "", "", fmt.Errorf("token refresh returned no access token")
	}
	nextAuth := w.auth
	nextAuth.AccessToken = resp.AccessToken
	if resp.IDToken != "" {
		nextAuth.IDToken = resp.IDToken
	}
	if resp.RefreshToken != "" {
		nextAuth.RefreshToken = resp.RefreshToken
	}
	if nextAuth.AccountID == "" {
		nextAuth.AccountID = chatGPTAccountID(nextAuth.IDToken)
	}
	if resp.RefreshToken != "" && w.persist != nil {
		raw, err := json.Marshal(nextAuth)
		if err != nil {
			return "", "", fmt.Errorf("encoding refreshed ChatGPT credentials: %w", err)
		}
		if err := w.persist(map[string]string{"chatgpt_auth": string(raw)}); err != nil {
			return "", "", fmt.Errorf("saving refreshed ChatGPT credentials: %w", err)
		}
	}
	w.auth = nextAuth
	if resp.ExpiresIn > 0 {
		w.exp = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	} else {
		w.exp = jwtExpiry(w.auth.AccessToken)
	}
	return w.auth.AccessToken, w.auth.AccountID, nil
}

// Models validates the credentials by refreshing once and serves the fixed
// catalog — the probe and Test button exercise the auth path, which is the
// part that can actually be wrong.
func (w *codexWire) Models(ctx context.Context) ([]string, error) {
	if _, _, err := w.token(ctx); err != nil {
		return nil, err
	}
	return append([]string(nil), codexModels...), nil
}

// Chat runs one invocation over the Responses API. The backend only streams,
// so the reply is assembled from the SSE events: each completed output item
// contributes text or a tool call, and response.completed carries the usage.
func (w *codexWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return w.chat(ctx, req, nil)
}

// ChatStream forwards Responses API output-text deltas while retaining the
// complete event sequence for the Transcript output.
func (w *codexWire) ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	return w.chat(ctx, req, emit)
}

func (w *codexWire) chat(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	access, account, err := w.token(ctx)
	if err != nil {
		return nil, err
	}

	instructions := req.System
	if req.JSONOutput {
		if len(req.JSONSchema) > 0 {
			instructions = schemaInstruction(instructions, req.JSONSchema)
		} else if !strings.Contains(instructions, "JSON") {
			instructions = strings.TrimSpace(instructions + "\n\nRespond with a single JSON object and nothing else.")
		}
	}
	if strings.TrimSpace(instructions) == "" {
		instructions = "You are a helpful assistant."
	}

	body := map[string]any{
		"model":               req.Model,
		"instructions":        instructions,
		"input":               codexInput(req.Messages),
		"tool_choice":         "auto",
		"parallel_tool_calls": false,
		"store":               false,
		"stream":              true,
		"include":             []any{},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name, "description": t.Description,
				"parameters": t.Schema, "strict": false,
			})
		}
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort, "summary": "auto"}
	}
	if req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", w.base+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+access)
	if account != "" {
		httpReq.Header.Set("chatgpt-account-id", account)
	}
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	// The backend gates on the requesting application; this is Codex CLI's
	// grant, so the wire identifies as it.
	httpReq.Header.Set("originator", "codex_cli_rs")
	httpReq.Header.Set("session_id", w.session)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if msg := apiErrorMessage(raw); msg != "" {
			return nil, fmt.Errorf("%s: %s", resp.Status, msg)
		}
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return parseCodexStream(resp.Body, emit)
}

// codexInput renders the conversation as Responses API input items: messages
// with typed content parts, tool calls as function_call items, and tool
// results as function_call_output items correlated by call_id.
func codexInput(messages []Message) []any {
	items := make([]any, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleTool:
			items = append(items, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Text,
			})
			// A tool's images ride a following user message, the same
			// placement the chat-completions wire uses: the API has no image
			// slot on a function output.
			if len(m.Images) > 0 {
				items = append(items, codexMessage(RoleUser, "", m.Images))
			}
		case RoleAssistant:
			replayed := false
			if raw := m.OpaqueState["openai-responses"]; len(raw) > 0 {
				var replay []json.RawMessage
				if json.Unmarshal(raw, &replay) == nil {
					for _, item := range replay {
						items = append(items, item)
					}
					replayed = true
				}
			}
			if m.Text != "" || len(m.ToolCalls) == 0 {
				items = append(items, codexMessage(RoleAssistant, m.Text, nil))
			}
			if replayed {
				continue
			}
			for _, call := range m.ToolCalls {
				args, err := json.Marshal(call.Args)
				if err != nil {
					args = []byte("{}")
				}
				items = append(items, map[string]any{
					"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(args),
				})
			}
		default:
			items = append(items, codexMessage(RoleUser, m.Text, m.Images))
		}
	}
	return items
}

// codexMessage builds one message input item. User content parts are
// input_text/input_image; assistant history is output_text, the type the API
// requires for what the model itself said.
func codexMessage(role, text string, images []Image) map[string]any {
	textType := "input_text"
	if role == RoleAssistant {
		textType = "output_text"
	}
	content := []map[string]any{}
	if text != "" || len(images) == 0 {
		content = append(content, map[string]any{"type": textType, "text": text})
	}
	for _, im := range images {
		content = append(content, map[string]any{"type": "input_image", "image_url": im.dataURL()})
	}
	return map[string]any{"type": "message", "role": role, "content": content}
}

// parseCodexStream assembles the response from the SSE events: completed
// output items carry the text and tool calls, response.completed the usage,
// and response.failed the provider's own reason.
func parseCodexStream(body io.Reader, emit func(string) error) (*ChatResponse, error) {
	out := &ChatResponse{}
	var text strings.Builder
	var summaries []string
	var replay []json.RawMessage
	var providerEvents []json.RawMessage
	providerBytes := 0
	completed := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), maxResponseBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Summary []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"summary"`
			} `json:"item"`
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					InputDetails struct {
						CachedTokens     int `json:"cached_tokens"`
						CacheWriteTokens int `json:"cache_write_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		providerBytes += len(data)
		if providerBytes > maxResponseBytes {
			return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
		}
		providerEvents = append(providerEvents, append(json.RawMessage(nil), data...))
		switch event.Type {
		case "response.output_text.delta":
			if emit != nil && event.Delta != "" {
				if err := emit(event.Delta); err != nil {
					return nil, err
				}
			}
		case "response.output_item.done":
			var envelope struct {
				Item json.RawMessage `json:"item"`
			}
			_ = json.Unmarshal([]byte(data), &envelope)
			if event.Item.Type == "reasoning" || event.Item.Type == "function_call" {
				replay = append(replay, envelope.Item)
			}
			switch event.Item.Type {
			case "message":
				for _, part := range event.Item.Content {
					if part.Type == "output_text" {
						text.WriteString(part.Text)
					}
				}
			case "function_call":
				args := map[string]any{}
				_ = json.Unmarshal([]byte(event.Item.Arguments), &args)
				out.ToolCalls = append(out.ToolCalls, ToolCall{
					ID: event.Item.CallID, Name: event.Item.Name, Args: args,
				})
			case "reasoning":
				for _, part := range event.Item.Summary {
					if part.Type == "summary_text" {
						summaries = append(summaries, part.Text)
					}
				}
			}
		case "response.completed":
			out.Usage.InputTokens = event.Response.Usage.InputTokens
			out.Usage.OutputTokens = event.Response.Usage.OutputTokens
			out.Usage.CachedInputTokens = event.Response.Usage.InputDetails.CachedTokens
			out.Usage.CacheWriteInputTokens = event.Response.Usage.InputDetails.CacheWriteTokens
			completed = true
		case "response.failed":
			msg := event.Response.Error.Message
			if msg == "" {
				msg = "response failed"
			}
			return nil, fmt.Errorf("codex backend: %s", msg)
		case "error":
			if event.Message != "" {
				return nil, fmt.Errorf("codex backend: %s", event.Message)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}
	if !completed && text.Len() == 0 && len(out.ToolCalls) == 0 {
		return nil, fmt.Errorf("stream ended without a response")
	}
	out.Text = text.String()
	out.ReasoningSummary = joinReasoningSummary(summaries)
	if raw, err := json.Marshal(replay); err == nil {
		if raw = boundedOpaque(raw); raw != nil && len(replay) > 0 {
			out.OpaqueState = map[string]json.RawMessage{"openai-responses": raw}
		}
	}
	if len(out.ToolCalls) > 0 {
		out.StopReason = "tool_calls"
	} else {
		out.StopReason = "stop"
	}
	if raw, err := json.Marshal(providerEvents); err == nil {
		out.ProviderResponse = raw
	}
	return out, nil
}
