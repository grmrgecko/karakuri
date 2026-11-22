package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSchema = map[string]any{
	"type":                 "object",
	"required":             []any{"answer"},
	"additionalProperties": false,
	"properties":           map[string]any{"answer": map[string]any{"type": "string"}},
}

// TestSchemaPlacementPerWire pins where a response schema lands on each wire:
// OpenAI's json_schema response format, Gemini's responseSchema reduced to
// the fields its API accepts, and Anthropic's system prompt, since the
// messages API has no JSON switch at all.
func TestSchemaPlacementPerWire(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]any{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		switch {
		case r.URL.Path == "/v1/chat/completions":
			w.Write([]byte(`{"choices": [{"message": {"content": "{}"}}]}`))
		case r.URL.Path == "/v1/messages":
			w.Write([]byte(`{"content": [{"type": "text", "text": "{}"}]}`))
		default:
			w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "{}"}]}}]}`))
		}
	}))
	defer srv.Close()

	req := ChatRequest{
		Model:      "m",
		Messages:   []Message{{Role: RoleUser, Text: "go"}},
		JSONOutput: true,
		JSONSchema: testSchema,
	}

	openai := &openaiWire{base: srv.URL + "/v1", maxTokensKey: "max_tokens"}
	_, err := openai.Chat(context.Background(), req)
	require.NoError(t, err)
	format := got["response_format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Equal(t, "response",
		format["json_schema"].(map[string]any)["name"])

	anthropic := &anthropicWire{base: srv.URL}
	_, err = anthropic.Chat(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, got["system"], "validates against this JSON Schema",
		"anthropic states the schema in the system prompt")
	assert.Contains(t, got["system"], `"answer"`)

	gemini := &geminiWire{base: srv.URL}
	_, err = gemini.Chat(context.Background(), req)
	require.NoError(t, err)
	gen := got["generationConfig"].(map[string]any)
	assert.Equal(t, "application/json", gen["responseMimeType"])
	schema := gen["responseSchema"].(map[string]any)
	assert.Contains(t, schema, "properties")
	assert.NotContains(t, schema, "additionalProperties",
		"fields Gemini's Schema object rejects are stripped")

	// With tools attached Gemini loses the native switch, so the schema moves
	// to the system prompt there too.
	withTools := req
	withTools.Tools = []ToolDef{{Name: "t", Description: "d"}}
	_, err = gemini.Chat(context.Background(), withTools)
	require.NoError(t, err)
	system := got["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	assert.Contains(t, system, "validates against this JSON Schema")
}
