package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT builds an unsigned-but-shaped JWT: the wire reads claims without
// verifying, so a fixture needs only the encoding to be right.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestParseCodexAuth pins the auth.json shapes Codex CLI writes: the
// {"tokens": {...}} envelope and the bare token object, with the account ID
// dug out of the id_token claim when the file does not carry it.
func TestParseCodexAuth(t *testing.T) {
	idToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-123"},
	})

	envelope := `{"OPENAI_API_KEY": null, "tokens": {"id_token": "` + idToken +
		`", "access_token": "at", "refresh_token": "rt", "account_id": ""}, "last_refresh": "2026-08-31T00:00:00Z"}`
	auth, err := parseCodexAuth(envelope)
	require.NoError(t, err, "the CLI's own auth.json layout must parse")
	assert.Equal(t, "rt", auth.RefreshToken)
	assert.Equal(t, "acct-123", auth.AccountID, "the account ID comes from the id_token claim when the file omits it")

	bare, err := parseCodexAuth(`{"refresh_token": "rt2", "account_id": "acct-9"}`)
	require.NoError(t, err, "a bare token object parses too")
	assert.Equal(t, "acct-9", bare.AccountID)

	_, err = parseCodexAuth(`{"tokens": {"access_token": "at"}}`)
	require.Error(t, err, "credentials without a refresh token cannot outlive the access token and are refused")
}

// TestCodexDeviceLogin pins the three OpenAI device-authorization requests
// used by Codex CLI: request a user code, wait on 404, then exchange the
// approved authorization code with the device callback and PKCE verifier.
func TestCodexDeviceLogin(t *testing.T) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, codexClientID, body["client_id"], "device authorization belongs to Codex CLI's public client")
		w.Write([]byte(`{"device_auth_id":"device-1","user_code":"ABCD-EFGH","interval":"3"}`))
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			http.Error(w, `{"error":"authorization_pending"}`, http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"authorization_code":"approved","code_verifier":"verifier"}`))
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "authorization_code", r.Form.Get("grant_type"), "the approved device code uses the authorization-code grant")
		assert.Equal(t, codexDeviceAuthCallback, r.Form.Get("redirect_uri"), "the exchange uses OpenAI's device callback")
		assert.Equal(t, "verifier", r.Form.Get("code_verifier"), "the exchange retains OpenAI's PKCE verifier")
		w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","id_token":"id"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oldCodeURL, oldPollURL, oldTokenURL := codexDeviceCodeURL, codexDeviceTokenURL, codexTokenURL
	codexDeviceCodeURL = srv.URL + "/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL = srv.URL + "/api/accounts/deviceauth/token"
	codexTokenURL = srv.URL + "/oauth/token"
	t.Cleanup(func() {
		codexDeviceCodeURL, codexDeviceTokenURL, codexTokenURL = oldCodeURL, oldPollURL, oldTokenURL
	})

	login, err := StartCodexDeviceLogin(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ABCD-EFGH", login.UserCode)
	assert.Equal(t, defaultDevicePollSeconds, login.Interval, "a shorter provider interval is clamped before browser polling")

	result, err := PollCodexDeviceLogin(context.Background(), login.DeviceAuthID, login.UserCode)
	require.NoError(t, err)
	assert.True(t, result.Pending, "404 means the user has not approved the code yet")

	result, err = PollCodexDeviceLogin(context.Background(), login.DeviceAuthID, login.UserCode)
	require.NoError(t, err)
	auth, err := parseCodexAuth(result.Credentials)
	require.NoError(t, err)
	assert.Equal(t, "refresh", auth.RefreshToken, "the durable refresh token is what the connection stores")
}

// TestCodexWireChat is the ChatGPT-account wire end to end against a fixture
// backend: the refresh grant runs first (Codex CLI's client ID, the pasted
// refresh token), the responses call carries the account and originator
// headers with the conversation as Responses-API input items, and the SSE
// stream assembles into text, a tool call, and usage.
func TestCodexWireChat(t *testing.T) {
	idToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-123"},
	})
	accessToken := makeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, codexClientID, body["client_id"], "the grant belongs to Codex CLI's client")
		assert.Equal(t, "refresh_token", body["grant_type"])
		assert.Equal(t, "rt-1", body["refresh_token"])
		json.NewEncoder(w).Encode(map[string]any{
			"id_token": idToken, "access_token": accessToken, "refresh_token": "rt-2", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+accessToken, r.Header.Get("Authorization"))
		assert.Equal(t, "acct-123", r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, "codex_cli_rs", r.Header.Get("originator"))
		assert.Equal(t, "responses=experimental", r.Header.Get("OpenAI-Beta"))

		var body struct {
			Model        string           `json:"model"`
			Instructions string           `json:"instructions"`
			Stream       bool             `json:"stream"`
			Store        bool             `json:"store"`
			Input        []map[string]any `json:"input"`
			Tools        []map[string]any `json:"tools"`
			Reasoning    map[string]any   `json:"reasoning"`
			CacheKey     string           `json:"prompt_cache_key"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "gpt-5", body.Model)
		assert.True(t, body.Stream, "the backend only streams")
		assert.False(t, body.Store, "conversations are not stored server-side")
		assert.Equal(t, "conversation-1", body.CacheKey, "Responses requests use the stable conversation cache key")
		assert.Equal(t, map[string]any{"effort": "high", "summary": "auto"}, body.Reasoning,
			"reasoning requests ask the Responses backend for its readable summary")
		require.Len(t, body.Input, 3,
			"user message, function_call, function_call_output — a tool-only assistant turn emits no message item")
		assert.Equal(t, "message", body.Input[0]["type"])
		assert.Equal(t, "function_call", body.Input[1]["type"])
		assert.Equal(t, "call_1", body.Input[1]["call_id"])
		assert.Equal(t, "function_call_output", body.Input[2]["type"])
		assert.Equal(t, "call_1", body.Input[2]["call_id"], "the observation correlates to its call")
		require.Len(t, body.Tools, 1)
		assert.Equal(t, "lookup", body.Tools[0]["name"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"checked manual"},{"type":"other","text":"ignore me"},{"type":"summary_text","text":"found fuse box"}]}}` + "\n\n"))
		w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"Check"}` + "\n\n"))
		w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"ing"}` + "\n\n"))
		w.Write([]byte(`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"Checking"}]}}` + "\n\n"))
		w.Write([]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_2","name":"lookup","arguments":"{\"q\":\"boiler\"}"}}` + "\n\n"))
		w.Write([]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":42,"output_tokens":7,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":4}}}}` + "\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldTokenURL := codexTokenURL
	codexTokenURL = srv.URL + "/oauth/token"
	t.Cleanup(func() { codexTokenURL = oldTokenURL })

	var persisted map[string]string
	w, err := newCodexWire(Config{
		Provider: ProviderOpenAI, Auth: AuthChatGPT, BaseURL: srv.URL,
		ChatGPTAuth: `{"tokens": {"id_token": "` + idToken + `", "refresh_token": "rt-1"}}`,
	}, func(secrets map[string]string) error {
		persisted = secrets
		return nil
	})
	require.NoError(t, err)

	var deltas []string
	streaming, ok := w.(streamingWire)
	require.True(t, ok, "the ChatGPT Responses wire must support streamed output")
	resp, err := streaming.ChatStream(context.Background(), ChatRequest{
		Model:           "gpt-5",
		CacheKey:        "conversation-1",
		System:          "Answer support questions.",
		ReasoningEffort: "high",
		Messages: []Message{
			{Role: RoleUser, Text: "Where is the fuse box?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Args: map[string]any{"q": "fuse"}}}},
			{Role: RoleTool, ToolCallID: "call_1", Text: "garage"},
		},
		Tools: []ToolDef{{Name: "lookup", Description: "search the manual",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, persisted["chatgpt_auth"], "a rotated refresh token must survive a server restart")
	persistedAuth, err := parseCodexAuth(persisted["chatgpt_auth"])
	require.NoError(t, err)
	assert.Equal(t, "rt-2", persistedAuth.RefreshToken, "OpenAI's rotated refresh token replaces the stored one")
	assert.Equal(t, []string{"Check", "ing"}, deltas, "Responses API text deltas must be forwarded before completion")
	assert.Equal(t, "Checking", resp.Text)
	require.Len(t, resp.ToolCalls, 1, "a completed function_call item becomes a tool call")
	assert.Equal(t, "call_2", resp.ToolCalls[0].ID)
	assert.Equal(t, map[string]any{"q": "boiler"}, resp.ToolCalls[0].Args)
	assert.Equal(t, 42, resp.Usage.InputTokens)
	assert.Equal(t, 7, resp.Usage.OutputTokens)
	assert.Equal(t, 30, resp.Usage.CachedInputTokens)
	assert.Equal(t, 4, resp.Usage.CacheWriteInputTokens)
	assert.Equal(t, "checked manual\n\nfound fuse box", resp.ReasoningSummary,
		"only summary_text entries become displayable reasoning")
	replay := codexInput([]Message{{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls, OpaqueState: resp.OpaqueState}})
	require.Len(t, replay, 3, "raw reasoning and function call replay beside the normalized text message")
	var reasoning, functionCall map[string]any
	require.NoError(t, json.Unmarshal(replay[0].(json.RawMessage), &reasoning))
	require.NoError(t, json.Unmarshal(replay[1].(json.RawMessage), &functionCall))
	assert.Equal(t, "reasoning", reasoning["type"])
	assert.Equal(t, "rs_1", reasoning["id"], "Responses reasoning items must replay with their provider ID")
	assert.Equal(t, "function_call", functionCall["type"])
	assert.Equal(t, "call_2", functionCall["call_id"], "the raw function call must not be reconstructed")

	models, err := w.Models(context.Background())
	require.NoError(t, err)
	assert.Contains(t, models, "gpt-5-codex", "the fixed catalog serves the picker with no backend listing")
}
