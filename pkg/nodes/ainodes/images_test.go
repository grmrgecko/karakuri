package ainodes_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// imageBranchNode is a tool-branch fixture that answers with bytes declaring
// an image format, the shape the screenshot node emits.
type imageBranchNode struct{ data []byte }

func (n *imageBranchNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "image-branch", Label: "Image Branch",
		Inputs:  []engine.PortSpec{{ID: "in", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortBinary}},
	}
}

func (n *imageBranchNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", &engine.Msg{
		Payload: n.data,
		Meta:    map[string]any{"format": "png"},
		Group:   inputs["in"].Group,
	}), nil
}

// TestAgentAttachesBinaryPayload covers vision input: with attach image on, a
// binary payload declaring its format arrives at the model as an image part
// beside the prompt text.
func TestAgentAttachesBinaryPayload(t *testing.T) {
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47}

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Len(t, body.Messages, 1)

		var parts []map[string]any
		require.NoError(t, json.Unmarshal(body.Messages[0].Content, &parts),
			"an image-bearing user turn is a parts array")
		require.Len(t, parts, 2, "prompt text plus one image")
		assert.Equal(t, "text", parts[0]["type"])
		assert.Equal(t, "What is on screen?", parts[0]["text"])
		url := parts[1]["image_url"].(map[string]any)["url"].(string)
		assert.True(t, strings.HasPrefix(url, "data:image/png;base64,"),
			"the declared format decides the MIME type")
		assert.Contains(t, url, base64.StdEncoding.EncodeToString(imageBytes))

		w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
			"content": "A desktop."
		}}], "usage": {"prompt_tokens": 10, "completion_tokens": 2}}`))
	}))
	defer llm.Close()

	llmID := uuid.New()
	llmDrv, err := ai.New(integrations.ConnModel{
		ID: llmID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"openai-compatible","base_url":"` + llm.URL + `/v1"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(sink)
	e := engine.New(engine.Options{
		Registry: reg,
		Clock:    engine.NewFakeClock(time.Now()),
		ConnResolver: func(id string) (any, error) {
			return map[string]any{llmID.String(): llmDrv}[id], nil
		},
	})
	e.Start()
	defer e.Stop()

	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	agentCfg, _ := json.Marshal(map[string]any{"prompt": "What is on screen?", "attach_image": true})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "a", Type: "ai-agent", Config: agentCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "a", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "a", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	h, err := e.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "vision-test", FlowVersionID: 1,
		TriggerNodeID: "t", Graph: g,
		InitialMsg: &engine.Msg{Payload: imageBytes, Meta: map[string]any{"format": "png"}},
	})
	require.NoError(t, err)
	select {
	case <-h.Done():
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the run never settled")
	}
	snap := h.Snapshot()
	require.Equalf(t, "succeeded", snap.Status, "run must succeed: %s", snap.Error)
	out := <-sink.got
	assert.Equal(t, "A desktop.", out.Payload)
}

// TestBranchToolReturnsImage covers the vision tool loop: a graph-backed tool
// whose branch returns declared image bytes hands the model the picture, not
// a wall of base64 text.
func TestBranchToolReturnsImage(t *testing.T) {
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d}

	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		switch calls.Add(1) {
		case 1:
			w.Write([]byte(`{"choices": [{"finish_reason": "tool_calls", "message": {
				"tool_calls": [{"id": "call_1", "function": {"name": "take_screenshot", "arguments": "{}"}}]
			}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`))
		default:
			// The observation is a tool turn saying an image came back, and
			// the image itself rides the following user turn — the only slot
			// chat-completions gives it.
			toolTurn := body.Messages[len(body.Messages)-2]
			require.Equal(t, "tool", toolTurn.Role)
			assert.Contains(t, string(toolTurn.Content), "image(s) returned")
			imageTurn := body.Messages[len(body.Messages)-1]
			require.Equal(t, "user", imageTurn.Role)
			var parts []map[string]any
			require.NoError(t, json.Unmarshal(imageTurn.Content, &parts))
			found := false
			for _, part := range parts {
				if part["type"] == "image_url" {
					url := part["image_url"].(map[string]any)["url"].(string)
					assert.True(t, strings.HasPrefix(url, "data:image/png;base64,"))
					found = true
				}
			}
			assert.True(t, found, "the branch's bytes must arrive as an image part")
			w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
				"content": "The screen shows a desktop."
			}}], "usage": {"prompt_tokens": 20, "completion_tokens": 6}}`))
		}
	}))
	defer llm.Close()

	llmID := uuid.New()
	llmDrv, err := ai.New(integrations.ConnModel{
		ID: llmID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"openai-compatible","base_url":"` + llm.URL + `/v1"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(&imageBranchNode{data: imageBytes})
	reg.Register(sink)
	e := engine.New(engine.Options{
		Registry: reg,
		Clock:    engine.NewFakeClock(time.Now()),
		ConnResolver: func(id string) (any, error) {
			return map[string]any{llmID.String(): llmDrv}[id], nil
		},
	})
	e.Start()
	defer e.Stop()

	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	toolCfg, _ := json.Marshal(map[string]any{
		"name": "take_screenshot", "description": "capture the screen",
	})
	agentCfg, _ := json.Marshal(map[string]any{"prompt": "What is on screen?"})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "tool", Type: flow.NodeTypeTool, Config: toolCfg},
			{ID: "b", Type: "image-branch"},
			{ID: "a", Type: "ai-agent", Config: agentCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "a", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "tool", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("tools"), Kind: flow.EdgeValue},
			{ID: "e4", Source: "tool", SourceHandle: flow.ToolCallPort, Target: "b", TargetHandle: "in"},
			{ID: "e5", Source: "b", SourceHandle: "out", Target: "tool", TargetHandle: flow.ToolReturnPort},
			{ID: "e6", Source: "a", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	h, err := e.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "vision-tool-test", FlowVersionID: 1,
		TriggerNodeID: "t", Graph: g,
		InitialMsg: &engine.Msg{Payload: "look"},
	})
	require.NoError(t, err)
	select {
	case <-h.Done():
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the run never settled")
	}
	snap := h.Snapshot()
	require.Equalf(t, "succeeded", snap.Status, "run must succeed: %s", snap.Error)
	out := <-sink.got
	assert.Equal(t, "The screen shows a desktop.", out.Payload)
}

// binarySource is a pure fixture emitting fixed bytes, standing in for a
// screenshot or a file read as binary on a value edge.
type binarySource struct{ data []byte }

func (b binarySource) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "binary-source", Label: "Binary source", Pure: true,
		Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortBinary}},
	}
}

func (b binarySource) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", &engine.Msg{Payload: b.data}), nil
}

// TestAgentAttachesWiredImages covers the multi-image path: two Image
// values pulled over the agent's Images socket land as image_url parts on
// the prompt turn in wire order, after the attach_image payload — the order
// the prompt's "image 1, image 2" prose relies on. The handle's declared MIME
// survives the value edge, which is the reason the AI Image node exists at
// all: a pull strips the metadata a producer declares its type in.
func TestAgentAttachesWiredImages(t *testing.T) {
	payloadBytes := []byte("payload-png")
	wireA := []byte("camera-jpeg")
	wireB := []byte("floorplan-png")

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		var parts []map[string]any
		require.NoError(t, json.Unmarshal(body.Messages[0].Content, &parts))
		require.Len(t, parts, 4, "prompt text, the payload image, then the two wired images")
		urls := make([]string, 0, 3)
		for _, part := range parts[1:] {
			urls = append(urls, part["image_url"].(map[string]any)["url"].(string))
		}
		assert.Contains(t, urls[0], base64.StdEncoding.EncodeToString(payloadBytes),
			"the attach_image payload leads")
		assert.True(t, strings.HasPrefix(urls[1], "data:image/jpeg;base64,"),
			"the handle's declared MIME survives the value edge")
		assert.Contains(t, urls[1], base64.StdEncoding.EncodeToString(wireA))
		assert.Contains(t, urls[2], base64.StdEncoding.EncodeToString(wireB),
			"wired images arrive in edge order")

		w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
			"content": "Two rooms."
		}}], "usage": {"prompt_tokens": 10, "completion_tokens": 2}}`))
	}))
	defer llm.Close()

	llmID := uuid.New()
	llmDrv, err := ai.New(integrations.ConnModel{
		ID: llmID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"openai-compatible","base_url":"` + llm.URL + `/v1"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	reg.Register(core.ImageNode{})
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(sink)
	srcA, srcB := binarySource{data: wireA}, binarySource{data: wireB}
	regSrc := func(t string, s binarySource) {
		meta := s.Meta()
		meta.Type = t
		reg.Register(renamedNode{NodeType: s, meta: meta})
	}
	regSrc("src-a", srcA)
	regSrc("src-b", srcB)
	e := engine.New(engine.Options{
		Registry: reg,
		Clock:    engine.NewFakeClock(time.Now()),
		ConnResolver: func(id string) (any, error) {
			return map[string]any{llmID.String(): llmDrv}[id], nil
		},
	})
	e.Start()
	defer e.Stop()

	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	agentCfg, _ := json.Marshal(map[string]any{"prompt": "Compare image 1 and image 2.", "attach_image": true})
	jpegCfg, _ := json.Marshal(map[string]any{"mime": "image/jpeg"})
	pngCfg, _ := json.Marshal(map[string]any{"mime": "image/png"})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "sa", Type: "src-a"},
			{ID: "sb", Type: "src-b"},
			{ID: "ia", Type: "image", Config: jpegCfg},
			{ID: "ib", Type: "image", Config: pngCfg},
			{ID: "a", Type: "ai-agent", Config: agentCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "a", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "sa", SourceHandle: "out", Target: "ia",
				TargetHandle: flow.ParamPortID("source"), Kind: flow.EdgeValue},
			{ID: "e4", Source: "sb", SourceHandle: "out", Target: "ib",
				TargetHandle: flow.ParamPortID("source"), Kind: flow.EdgeValue},
			{ID: "e5", Source: "ia", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("images"), Kind: flow.EdgeValue},
			{ID: "e6", Source: "ib", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("images"), Kind: flow.EdgeValue},
			{ID: "e7", Source: "a", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	h, err := e.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "images-test", FlowVersionID: 1,
		TriggerNodeID: "t", Graph: g,
		InitialMsg: &engine.Msg{Payload: payloadBytes, Meta: map[string]any{"format": "png"}},
	})
	require.NoError(t, err)
	select {
	case <-h.Done():
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the run never settled")
	}
	snap := h.Snapshot()
	require.Equalf(t, "succeeded", snap.Status, "the multi-image run must settle: %s", snap.Error)
	out := <-sink.got
	assert.Equal(t, "Two rooms.", out.Payload)
}

// renamedNode lets one fixture implementation register under two type names,
// so a graph can hold two distinct pure sources.
type renamedNode struct {
	engine.NodeType
	meta engine.NodeMeta
}

func (r renamedNode) Meta() engine.NodeMeta { return r.meta }
