package flow

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPromptGraphValidation protects stable wired choices and checks attachment
// data on draft saves as well as deployment, where unsafe files cannot hide.
func TestPromptGraphValidation(t *testing.T) {
	reg := stubRegistry{
		"browser-prompt": {Type: NodeTypeBrowserPrompt, Inputs: []PortSpec{{ID: "in", Type: PortAny}}, DynamicPorts: true, ConfigSpec: []FieldSpec{{ID: "markdown", Type: FieldMarkdown}, {ID: "attachments", Type: FieldAttachments}, {ID: "buttons", Type: FieldButtons}}},
		"debug":          {Type: "debug", Inputs: []PortSpec{{ID: "in", Type: PortAny}}},
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	imageFile := Attachment{Filename: "location.png", Type: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}
	for _, tc := range []struct {
		name       string
		buttons    []PromptButton
		attachment Attachment
		valid      bool
	}{
		{"renamed choice keeps wiring", []PromptButton{{ID: "retry", Label: "Try again"}}, imageFile, true},
		{"removed choice loses wiring", []PromptButton{{ID: "bypass", Label: "Skip"}}, imageFile, false},
		{"duplicate id is ambiguous", []PromptButton{{ID: "retry", Label: "Retry"}, {ID: "retry", Label: "Skip"}}, imageFile, false},
		{"error is reserved", []PromptButton{{ID: "error", Label: "Stop"}}, imageFile, false},
		{"HTML cannot impersonate PNG", []PromptButton{{ID: "retry", Label: "Retry"}}, Attachment{Filename: "location.png", Type: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("<html>untrusted</html>"))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := json.Marshal(map[string]any{"markdown": "![Location](attachment:location.png)", "buttons": tc.buttons, "attachments": map[string]Attachment{"location.png": tc.attachment}})
			require.NoError(t, err)
			graph := &Graph{Nodes: []Node{{ID: "prompt", Type: NodeTypeBrowserPrompt, Config: cfg}, {ID: "debug", Type: "debug"}}, Edges: []Edge{{ID: "chosen", Source: "prompt", SourceHandle: "retry", Target: "debug", TargetHandle: "in"}}}
			for _, lenient := range []bool{false, true} {
				var err error
				if lenient {
					err = ValidateDraft(graph, reg, nil, false)
				} else {
					err = ValidateFull(graph, reg, nil, false)
				}
				assert.Equal(t, tc.valid, err == nil, "choice identity and attachment safety apply to drafts and deployments")
			}
		})
	}
}
