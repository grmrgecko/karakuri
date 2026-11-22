package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/browserprompt"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promptTestBrowser replaces only the OS browser-launch boundary.
type promptTestBrowser struct{ launch func(string) error }

func (promptTestBrowser) Meta() engine.NodeMeta {
	return engine.NodeMeta{Inputs: []engine.PortSpec{{ID: "in", Type: flow.PortAny}}, Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortAny}}}
}

// TestBrowserPromptGraphRouting proves a human wait releases the only worker
// and that answers and failures use their wired branches in the same run.
func TestBrowserPromptGraphRouting(t *testing.T) {
	for _, mode := range []string{"answer", "timeout", "launch failure"} {
		t.Run(mode, func(t *testing.T) {
			manager := browserprompt.NewManager()
			launched := make(chan string, 1)
			browser := promptTestBrowser{launch: func(target string) error {
				u, err := url.Parse(target)
				if err != nil {
					return err
				}
				launched <- u.Fragment
				if mode == "launch failure" {
					return errors.New("desktop unavailable")
				}
				return nil
			}}
			reg := engine.NewRegistry()
			reg.Register(ManualNode{})
			reg.Register(DelayNode{})
			reg.Register(DebugNode{})
			reg.Register(BrowserPromptNode{manager: manager, lookup: func(string) (engine.NodeType, bool) { return browser, true }})
			events := make(chan *mq.Message, 128)
			sub := mq.B.Subscribe("run:*", func(msg *mq.Message) { events <- msg })
			t.Cleanup(func() { mq.B.Unsubscribe(sub) })
			e := engine.New(engine.Options{Registry: reg, Publisher: mq.B, MaxParallelNodes: 1})
			t.Cleanup(e.Stop)
			timeout := "5s"
			if mode == "timeout" {
				timeout = "1s"
			}
			cfg, err := json.Marshal(flow.BrowserPromptConfig{Agent: "test-host", BrowserBaseURL: "http://karakuri.test", Markdown: "Reconnect the pump.", Timeout: timeout})
			require.NoError(t, err)
			g := &flow.Graph{Nodes: []flow.Node{
				{ID: "start", Type: "manual"}, {ID: "prompt", Type: flow.NodeTypeBrowserPrompt, Config: cfg},
				{ID: "gate", Type: "delay", Config: json.RawMessage(`{"duration":"100ms"}`)},
				{ID: "parallel", Type: "debug"}, {ID: "bypass", Type: "debug"}, {ID: "retry", Type: "debug"}, {ID: "error", Type: "debug"},
			}, Edges: []flow.Edge{
				{ID: "question", Source: "start", SourceHandle: "out", Target: "prompt", TargetHandle: "in"},
				{ID: "fork", Source: "start", SourceHandle: "out", Target: "gate", TargetHandle: "in"},
				{ID: "work", Source: "gate", SourceHandle: "out", Target: "parallel", TargetHandle: "in"},
				{ID: "chosen", Source: "prompt", SourceHandle: "bypass", Target: "bypass", TargetHandle: "in"},
				{ID: "unchosen", Source: "prompt", SourceHandle: "retry", Target: "retry", TargetHandle: "in"},
				{ID: "failed", Source: "prompt", SourceHandle: "error", Target: "error", TargetHandle: "in"},
			}}
			h, err := e.RunFlow(engine.RunOptions{FlowID: uuid.New(), Graph: g, TriggerNodeID: "start", InitialMsg: &engine.Msg{Payload: "pump"}})
			require.NoError(t, err)
			var token string
			select {
			case token = <-launched:
			case <-time.After(2 * time.Second):
				require.FailNow(t, "the prompt must reach the browser launcher")
			}
			states := map[string]string{}
			readEvent := func(msg *mq.Message) {
				if msg.Topic != "run:"+h.ID.String() || msg.Type != "node_state" {
					return
				}
				var event struct {
					Node  string `json:"node_id"`
					State string `json:"state"`
				}
				require.NoError(t, json.Unmarshal(msg.Payload, &event))
				states[event.Node] = event.State
			}
			if mode == "answer" {
				deadline := time.NewTimer(2 * time.Second)
				defer deadline.Stop()
				for states["parallel"] != "succeeded" {
					select {
					case msg := <-events:
						readEvent(msg)
					case <-deadline.C:
						require.FailNow(t, "parallel work must finish while the human question is pending with one worker")
					}
				}
				_, err := manager.Read(token)
				require.NoError(t, err, "parallel work must not complete or cancel the question")
				require.NoError(t, manager.Answer(token, "bypass", ""))
			}
			select {
			case <-h.Done():
			case <-time.After(2 * time.Second):
				require.FailNow(t, "the run must settle after the answer or prompt failure")
			}
			for len(events) > 0 {
				readEvent(<-events)
			}
			assert.Equal(t, "succeeded", h.Snapshot().Status, "the wired Error branch handles prompt failures")
			assert.Equal(t, "succeeded", states["parallel"])
			assert.Equal(t, "skipped", states["retry"], "an unchosen output must not dispatch work")
			if mode == "answer" {
				assert.Equal(t, "succeeded", states["bypass"])
				assert.Equal(t, "skipped", states["error"])
			} else {
				assert.True(t, h.Snapshot().HadFailures)
				assert.Equal(t, "skipped", states["bypass"])
				assert.Equal(t, "succeeded", states["error"], "timeouts and failed browser launches must dispatch Error")
			}
		})
	}
}
func (n promptTestBrowser) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		URL string `json:"url"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	return nil, n.launch(cfg.URL)
}

// TestBrowserPromptMessageLifecycle keeps the failed envelope intact across a
// standalone engine execution and revokes the capability on answer or cancellation.
func TestBrowserPromptMessageLifecycle(t *testing.T) {
	for _, mode := range []string{"answer", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			manager := browserprompt.NewManager()
			launched := make(chan string, 1)
			browser := promptTestBrowser{launch: func(target string) error {
				u, err := url.Parse(target)
				if err != nil {
					return err
				}
				launched <- u.Fragment
				return nil
			}}
			reg := engine.NewRegistry()
			reg.Register(BrowserPromptNode{manager: manager, lookup: func(kind string) (engine.NodeType, bool) { return browser, kind == "agent.test-host.open-url" }})
			e := engine.New(engine.Options{Registry: reg})
			t.Cleanup(e.Stop)
			cfg := json.RawMessage(`{"agent":"test-host","browser_base_url":"http://karakuri.test:8080","markdown":"Fix {{meta.error}}","timeout":"5s","feedback":true}`)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			input := &engine.Msg{ID: "correlation", Payload: map[string]any{"job": "pump"}, Topic: "repair", Meta: map[string]any{"error": "pump disconnected", "attempts": 3}, Group: []engine.GroupFrame{{ID: "batch", Index: 1, Count: 2}}}
			done := make(chan *engine.NodeTestResult, 1)
			errors := make(chan error, 1)
			go func() {
				result, err := e.TestNode(ctx, engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "prompt", Type: flow.NodeTypeBrowserPrompt, Config: cfg}}}, StartNodeID: "prompt", SeedInputs: map[string]*engine.Msg{"in": input}})
				errors <- err
				done <- result
			}()
			var token string
			select {
			case token = <-launched:
			case <-time.After(time.Second):
				require.FailNow(t, "the agent browser must be asked to open the prompt")
			}
			if mode == "answer" {
				view, err := manager.Read(token)
				require.NoError(t, err)
				assert.Contains(t, view.HTML, "pump disconnected", "instructions interpolate against the failed message")
				require.NoError(t, manager.Answer(token, "bypass", "reconnected hose"))
			} else {
				cancel()
			}
			select {
			case result := <-done:
				require.NoError(t, <-errors)
				require.NotNil(t, result)
				if mode == "answer" {
					require.Equal(t, "succeeded", result.Status, result.Error)
					require.Len(t, result.Outputs, 1, "only the chosen branch may continue")
					require.Len(t, result.Outputs["bypass"], 1)
					var output engine.Msg
					require.NoError(t, json.Unmarshal(result.Outputs["bypass"][0], &output))
					assert.Equal(t, input.ID, output.ID)
					assert.Equal(t, input.Payload, output.Payload)
					assert.Equal(t, input.Topic, output.Topic)
					assert.Equal(t, input.Group, output.Group)
					assert.Equal(t, input.Meta["error"], output.Meta["error"])
					assert.EqualValues(t, input.Meta["attempts"], output.Meta["attempts"])
					assert.Equal(t, "reconnected hose", output.Meta["browser_prompt"].(map[string]any)["text"])
					assert.NotContains(t, input.Meta, "browser_prompt", "answering must not mutate upstream metadata")
				} else {
					assert.Empty(t, result.Outputs)
					assert.Equal(t, "canceled", result.Status)
					assert.Contains(t, result.Error, "canceled")
				}
			case <-time.After(2 * time.Second):
				require.FailNow(t, "the prompt must complete on answer or cancellation")
			}
			_, err := manager.Read(token)
			assert.ErrorIs(t, err, browserprompt.ErrGone, "every completion path revokes the capability")
		})
	}
}
