package mcpserver_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/mcpserver"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssistantStreamsScopedEdits drives the authenticated editor endpoint
// through a synthetic Chat Completions SSE peer and the real MCP handlers.
// A second tool round deploys the selected flow and attempts a cross-document
// write. Deployment succeeds; the write to another flow remains refused.
func TestAssistantStreamsScopedEdits(t *testing.T) {
	srv, token, gatedToken := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, first := callTool(t, session, "create_flow", map[string]any{"name": "Selected"})
	_, other := callTool(t, session, "create_flow", map[string]any{"name": "Other"})
	traceDir := t.TempDir()
	config.C.MCP.AssistantTraceDir = traceDir
	id := first["id"].(string)
	otherID := other["id"].(string)
	round := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
			return
		}
		if r.Method == http.MethodGet {
			// Context window probes for llama.cpp props find nothing here.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			Messages []map[string]any `json:"messages"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		for _, tool := range request.Tools {
			assert.NotEqual(t, "delete_flow", tool.Function.Name)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"content": "Draft updated."}
		if round == 0 {
			args, err := json.Marshal(map[string]any{"id": id, "graph": validGraph()})
			require.NoError(t, err)
			delta = map[string]any{"content": "Editing the graph.", "tool_calls": []any{map[string]any{"index": 0, "id": "save", "type": "function", "function": map[string]any{"name": "save_flow_graph", "arguments": string(args)}}}}
		} else if round == 1 {
			delta = map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "escape", "type": "function", "function": map[string]any{"name": "update_flow", "arguments": fmt.Sprintf(`{"id":%q,"name":"Wrong"}`, otherID)}},
				map[string]any{"index": 1, "id": "deploy", "type": "function", "function": map[string]any{"name": "deploy_flow", "arguments": fmt.Sprintf(`{"id":%q}`, id)}},
			}}
		} else {
			observations := 0
			for _, message := range request.Messages {
				if message["role"] == "tool" {
					observations++
				}
			}
			assert.Equal(t, 3, observations, "the next model round receives successful and refused tool results")
		}
		round++
		raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		require.NoError(t, err)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	t.Cleanup(peer.Close)
	integrations.RegisterKind(ai.KindDef())
	t.Cleanup(func() { integrations.UnregisterKind(ai.KindAI) })
	config.C.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	connection := &models.Connection{Name: "Fixture provider", Kind: ai.KindAI, Enabled: true}
	require.NoError(t, connection.SetConfig(ai.Config{Provider: ai.ProviderOpenAICompat, BaseURL: peer.URL}))
	require.NoError(t, db.DB.Create(connection).Error)
	manager := connmgr.New(nil, nil)
	connops.SetManager(manager)
	t.Cleanup(func() { manager.Stop(); connops.SetManager(nil) })
	require.NoError(t, manager.Start())
	handler := httptest.NewServer(middleware.Auth()(mcpserver.AssistantHandler()))
	t.Cleanup(handler.Close)
	body, err := json.Marshal(map[string]any{"kind": "flow", "id": id, "connection_id": connection.ID, "model": "fixture", "prompt": "Add a manual delay debug chain and deploy it.", "allow_execution": true})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, handler.URL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "application/x-ndjson", response.Header.Get("Content-Type"))
	var events []map[string]any
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var event map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, events)
	assert.Equal(t, "done", events[len(events)-1]["type"], "the agent returns replayable history after its final response")
	changed, failed := 0, 0
	for _, event := range events {
		if event["changed"] == true {
			changed++
		}
		if event["failed"] == true {
			failed++
		}
	}
	traces, err := filepath.Glob(filepath.Join(traceDir, "*.ndjson"))
	require.NoError(t, err)
	require.Len(t, traces, 1)
	fileInfo, err := os.Stat(traces[0])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())
	trace, err := os.ReadFile(traces[0])
	require.NoError(t, err)
	assert.NotContains(t, string(trace), token, "traces must not contain HTTP bearer credentials")
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(trace), []byte("\n")) {
		var record map[string]any
		require.NoError(t, json.Unmarshal(line, &record))
		records = append(records, record)
	}
	assert.Equal(t, "turn.start", records[0]["type"])
	assert.Equal(t, "turn.end", records[len(records)-1]["type"])
	for _, record := range records {
		if record["type"] == "model.response" {
			captured := record["data"].(map[string]any)["response"].(map[string]any)
			assert.NotEmpty(t, captured["ProviderResponse"], "retain the provider events before interpreting tool arguments")
		}
	}
	assert.Equal(t, 2, changed)
	assert.Equal(t, 1, failed)
	_, stored := callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.Len(t, stored["graph"].(map[string]any)["nodes"], 3)
	assert.Equal(t, true, stored["deployed"])
	_, untouched := callTool(t, session, "get_flow", map[string]any{"id": otherID})
	assert.Equal(t, "Other", untouched["name"])
	// Reuse the same provider's requested deployment with execution disabled.
	round = 0
	var draftOnly map[string]any
	require.NoError(t, json.Unmarshal(body, &draftOnly))
	draftOnly["allow_execution"] = false
	draftBody, err := json.Marshal(draftOnly)
	require.NoError(t, err)
	draftRequest, err := http.NewRequest(http.MethodPost, handler.URL, bytes.NewReader(draftBody))
	require.NoError(t, err)
	draftRequest.Header.Set("Authorization", "Bearer "+token)
	draftResponse, err := http.DefaultClient.Do(draftRequest)
	require.NoError(t, err)
	draftStream, err := io.ReadAll(draftResponse.Body)
	require.NoError(t, err)
	require.NoError(t, draftResponse.Body.Close())
	assert.Contains(t, string(draftStream), `tool \"deploy_flow\" is not available in this editor`)
	_, versions := callTool(t, session, "list_flow_versions", map[string]any{"id": id})
	assert.Len(t, versions["versions"], 1, "a model must not deploy when execution is disabled, even if it calls a hidden tool")

	req, err = http.NewRequest(http.MethodPost, handler.URL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gatedToken)
	response, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
}

// TestAssistantRoundLimitPreservesReplay drives the HTTP endpoint through a
// synthetic SSE provider and real MCP tools, then resubmits the returned history.
func TestAssistantRoundLimitPreservesReplay(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, flow := callTool(t, session, "create_flow", map[string]any{"name": "Budget"})
	id := flow["id"].(string)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
			return
		}
		if r.Method == http.MethodGet {
			// Context window probes for llama.cpp props find nothing here.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		observations := 0
		for _, message := range request.Messages {
			if message["role"] == "tool" {
				observations++
			}
		}
		last := request.Messages[len(request.Messages)-1]
		delta := map[string]any{"content": "Finished."}
		if last["content"] == "Continue" {
			assert.Equal(t, 50, observations, "continuation must include every completed tool result")
			assert.Equal(t, "Keep inspecting", request.Messages[1]["content"], "continuation must retain the original user request")
		} else if request.Messages[1]["content"] != "Finish after 21 tools" || observations < 21 {
			delta = map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": fmt.Sprintf("read-%d", observations), "type": "function",
				"function": map[string]any{"name": "get_flow", "arguments": fmt.Sprintf(`{"id":%q}`, id)},
			}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		require.NoError(t, err)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	t.Cleanup(peer.Close)
	integrations.RegisterKind(ai.KindDef())
	t.Cleanup(func() { integrations.UnregisterKind(ai.KindAI) })
	config.C.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	connection := &models.Connection{Name: "Budget provider", Kind: ai.KindAI, Enabled: true}
	require.NoError(t, connection.SetConfig(ai.Config{Provider: ai.ProviderOpenAICompat, BaseURL: peer.URL}))
	require.NoError(t, db.DB.Create(connection).Error)
	manager := connmgr.New(nil, nil)
	connops.SetManager(manager)
	t.Cleanup(func() { manager.Stop(); connops.SetManager(nil) })
	require.NoError(t, manager.Start())
	handler := httptest.NewServer(middleware.Auth()(mcpserver.AssistantHandler()))
	t.Cleanup(handler.Close)
	var replay any
	for _, tc := range []struct {
		prompt string
		tools  int
		notice bool
	}{
		{"Finish after 21 tools", 21, false},
		{"Keep inspecting", 50, true},
		{"Continue", 0, false},
	} {
		body, err := json.Marshal(map[string]any{"kind": "flow", "id": id, "connection_id": connection.ID, "model": "fixture", "prompt": tc.prompt, "messages": replay})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, handler.URL, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		var last map[string]any
		tools := 0
		for scanner.Scan() {
			var event map[string]any
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
			assert.NotEqual(t, "error", event["type"], "the budget boundary must return replayable history")
			if event["type"] == "tool.result" {
				tools++
				assert.Equal(t, false, event["failed"])
			}
			last = event
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, response.Body.Close())
		assert.Equal(t, tc.tools, tools, "tool count for %s", tc.prompt)
		require.Equal(t, "done", last["type"])
		if tc.notice {
			assert.Contains(t, last["notice"], "50-round limit")
			replay = last["messages"]
			messages := replay.([]any)
			assert.Len(t, messages, 101, "one user message plus 50 assistant/tool pairs must be replayable")
			assert.Equal(t, "tool", messages[len(messages)-1].(map[string]any)["Role"], "the final tool call must have its result before pausing")
		} else {
			assert.NotContains(t, last, "notice")
		}
	}
}

// TestAssistantCompactsPastContextWindow replays the 2026-11-08 Kuri
// failure through the real endpoint: Kuri's own system prompt and tool
// schemas on a 32,768-token window, with a peer that counts four characters
// per token and rejects larger requests with llama.cpp's overflow text. Ten
// get_flow rounds of about 2,000 tokens each must finish the turn with a
// context.compacted event, no rejected request, and replay history that
// starts from the checkpoint.
func TestAssistantCompactsPastContextWindow(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	// get_flow returns the description each round: about 2,000 tokens.
	_, flow := callTool(t, session, "create_flow", map[string]any{"name": "Office light",
		"description": strings.Repeat("light.office_workbench and light.office_lamp follow light.office. ", 120)})
	id := flow["id"].(string)
	const window = 32768
	var mu sync.Mutex
	served, rejected, summaries := 0, 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools  json.RawMessage `json:"tools"`
			Stream bool            `json:"stream"`
		}
		require.NoError(t, json.Unmarshal(raw, &request))
		// Tool schemas count toward the prompt, as llama.cpp renders them.
		chars, last := len(request.Tools), ""
		for _, m := range request.Messages {
			var text string
			_ = json.Unmarshal(m.Content, &text)
			chars += len([]rune(text))
			if m.Role == "user" {
				last = text
			}
		}
		tokens := chars / 4
		mu.Lock()
		defer mu.Unlock()
		if tokens > window {
			rejected++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":{"code":400,"message":"request (%d tokens) exceeds the available context size (%d tokens), try increasing it"}}`, tokens, window)
			return
		}
		delta := map[string]any{"content": "Linked the office light to the workbench light and lamp."}
		switch {
		case strings.Contains(last, "You MUST summarize the conversation above") || strings.Contains(last, "Update the existing checkpoint"):
			summaries++
			delta = map[string]any{"content": "## Objective\n- Link the office light to the workbench light and lamp.\n\n## Next Move\n1. Patch the flow."}
		case served < 10:
			served++
			delta = map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": fmt.Sprintf("type-%d", served), "type": "function",
				"function": map[string]any{"name": "get_flow", "arguments": fmt.Sprintf(`{"id":%q}`, id)},
			}}}
		}
		if !request.Stream {
			reply, err := json.Marshal(map[string]any{
				"choices": []any{map[string]any{"finish_reason": "stop", "message": delta}},
				"usage":   map[string]any{"prompt_tokens": tokens, "completion_tokens": 10},
			})
			require.NoError(t, err)
			_, _ = w.Write(reply)
			return
		}
		chunk, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":10}}\n\ndata: [DONE]\n\n", chunk, tokens)
	}))
	t.Cleanup(peer.Close)
	integrations.RegisterKind(ai.KindDef())
	t.Cleanup(func() { integrations.UnregisterKind(ai.KindAI) })
	config.C.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	connection := &models.Connection{Name: "Small window", Kind: ai.KindAI, Enabled: true}
	require.NoError(t, connection.SetConfig(ai.Config{Provider: ai.ProviderOpenAICompat, BaseURL: peer.URL, ContextWindow: window}))
	require.NoError(t, db.DB.Create(connection).Error)
	manager := connmgr.New(nil, nil)
	connops.SetManager(manager)
	t.Cleanup(func() { manager.Stop(); connops.SetManager(nil) })
	require.NoError(t, manager.Start())
	handler := httptest.NewServer(middleware.Auth()(mcpserver.AssistantHandler()))
	t.Cleanup(handler.Close)

	body, err := json.Marshal(map[string]any{"kind": "flow", "id": id, "connection_id": connection.ID, "model": "gemma-4-26B",
		"prompt": "When I turn on my office light, also turn on the workbench light and lamp."})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, handler.URL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	var last map[string]any
	compacted := 0
	for scanner.Scan() {
		var event map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		require.NotEqual(t, "error", event["type"], "the turn must not overflow: %v", event["message"])
		if event["type"] == "context.compacted" {
			compacted++
			assert.Equal(t, "threshold", event["reason"])
		}
		last = event
	}
	require.NoError(t, scanner.Err())
	require.NoError(t, response.Body.Close())

	assert.Positive(t, compacted, "ten multi-kilobyte tool rounds over Kuri's prompt must compact")
	assert.Zero(t, rejected, "no request may exceed the window")
	assert.Equal(t, compacted, summaries, "each compaction is one summary request")
	require.Equal(t, "done", last["type"])
	messages := last["messages"].([]any)
	require.NotEmpty(t, messages)
	assert.True(t, strings.HasPrefix(messages[0].(map[string]any)["Text"].(string), "<conversation-checkpoint>"),
		"the replay history continues from the checkpoint")
}

// TestMCPDashboardLayout keeps section and widget writes on the dashboard
// named by the caller, including when both dashboards belong to that caller.
func TestMCPDashboardLayout(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, first := callTool(t, session, "create_dashboard", map[string]any{"name": "Layout", "layout_mode": "scroll"})
	_, second := callTool(t, session, "create_dashboard", map[string]any{"name": "Other layout", "layout_mode": "scroll"})
	id := first["id"].(string)
	otherID := second["id"].(string)
	theme := models.Theme{Name: "Fixture light", PackageName: "fixture-light", Variables: `{"--theme-base":"#fff"}`}
	require.NoError(t, db.DB.Create(&theme).Error)
	themeResult, themes := callTool(t, session, "list_themes", map[string]any{})
	require.False(t, themeResult.IsError)
	require.Len(t, themes["themes"], 1)
	assert.NotContains(t, themes["themes"].([]any)[0].(map[string]any), "variables", "theme selection must not fill a model context with CSS variables")
	themeResult, _ = callTool(t, session, "update_dashboard", map[string]any{"id": id, "theme_id": theme.ID})
	require.False(t, themeResult.IsError)
	_, themed := callTool(t, session, "get_dashboard", map[string]any{"id": id, "view": "draft"})
	assert.Equal(t, float64(theme.ID), themed["theme_id"])

	res, section := callTool(t, session, "add_dashboard_section", map[string]any{"dashboard_id": id, "title": "Lights"})
	require.False(t, res.IsError)
	sectionID := section["id"].(string)
	for _, target := range []struct {
		name, id string
		failed   bool
	}{{"other dashboard", otherID, true}, {"selected dashboard", id, false}} {
		res, _ = callTool(t, session, "update_dashboard_section", map[string]any{"dashboard_id": target.id, "section_id": sectionID, "title": "Lighting", "sort_order": 2})
		assert.Equal(t, target.failed, res.IsError, "section write for %s", target.name)
	}
	_, stored := callTool(t, session, "get_dashboard", map[string]any{"id": id, "view": "draft"})
	require.Len(t, stored["sections"], 1)
	assert.Equal(t, "Lighting", stored["sections"].([]any)[0].(map[string]any)["title"])

	var dashboard models.Dashboard
	require.NoError(t, db.DB.First(&dashboard, "id = ?", id).Error)
	widget := models.Widget{Name: "Fixture", PackageName: "fixture-layout", TagName: "fixture-layout"}
	require.NoError(t, db.DB.Create(&widget).Error)
	placement := models.DashboardWidget{DashboardID: dashboard.ID, WidgetID: widget.ID, Width: 2, Height: 2}
	require.NoError(t, db.DB.Create(&placement).Error)
	for _, tool := range []string{"update_dashboard_widget", "remove_dashboard_widget"} {
		res, _ = callTool(t, session, tool, map[string]any{"dashboard_id": otherID, "widget_id": placement.ID.String()})
		assert.True(t, res.IsError, "%s must refuse a placement from another dashboard", tool)
	}
	res, _ = callTool(t, session, "update_dashboard_widget", map[string]any{"dashboard_id": id, "widget_id": placement.ID.String(), "width": 3})
	require.False(t, res.IsError)
	require.NoError(t, db.DB.First(&placement, "id = ?", placement.ID).Error)
	assert.Equal(t, 3, placement.Width)
	res, _ = callTool(t, session, "remove_dashboard_widget", map[string]any{"dashboard_id": id, "widget_id": placement.ID.String()})
	require.False(t, res.IsError)
	var count int64
	require.NoError(t, db.DB.Model(&models.DashboardWidget{}).Where("id = ?", placement.ID).Count(&count).Error)
	assert.Zero(t, count)
	res, _ = callTool(t, session, "remove_dashboard_section", map[string]any{"dashboard_id": id, "section_id": sectionID})
	require.False(t, res.IsError)
	_, stored = callTool(t, session, "get_dashboard", map[string]any{"id": id, "view": "draft"})
	assert.Empty(t, stored["sections"])
}
