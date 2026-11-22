package hanodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"go.uber.org/zap"
)

const (
	assistRequestEvent   = "karakuri_assist_request"
	assistServiceDomain  = "karakuri_assist"
	assistRespondService = "respond"
)

// AssistNode accepts turns from the Karakuri Assist custom integration in
// Home Assistant over the selected connection's event subscription.
type AssistNode struct{}

func (AssistNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "ha-assist",
		Label:             "HA Assist",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderHomeAssistant,
		Description:       "Starts a run for each turn sent by a Home Assistant Assist pipeline",
		Icon:              "las la-comment",
		ConnectionKind:    homeassistant.Kind,
		TestWithoutParams: true,
		Inputs: []engine.PortSpec{
			{ID: "response", Label: "Response", Type: flow.PortAny, Kind: flow.PortKindReturn, Optional: true},
		},
		Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{
				ID: "agent_id", Label: "Agent ID", Type: flow.FieldString, Required: true,
				Default: "default", Placeholder: "default",
				Help: "Route name shared with the Karakuri Assist integration in Home Assistant.",
			},
			{
				ID: "test_text", Label: "Test text", Type: flow.FieldString,
				Default: "What time is it?",
				Help:    "Sentence used by Test Run. No request is sent from Home Assistant.",
			},
			{
				ID: "continue_conversation", Label: "Continue conversation", Type: flow.FieldSelect, Default: "auto",
				Options: []engine.Option{
					{Value: "auto", Label: "When the response is a question"},
					{Value: "ai", Label: "AI decides"},
					{Value: "yes", Label: "Always"},
					{Value: "no", Label: "Never"},
				},
				Help: "Controls whether Assist listens after speaking. AI decides reads response and continue_conversation from AI JSON.",
			},
		},
	}
}

type assistConfig struct {
	Connection           string `json:"connection"`
	AgentID              string `json:"agent_id"`
	TestText             string `json:"test_text"`
	ContinueConversation string `json:"continue_conversation"`
}

// AssistRequest is the event data written by the Home Assistant component.
// Transcript entries use the same role/text shape as the AI nodes.
type AssistRequest struct {
	RequestID      string         `json:"request_id"`
	AgentID        string         `json:"agent_id"`
	Text           string         `json:"text"`
	ConversationID string         `json:"conversation_id"`
	Language       string         `json:"language"`
	DeviceID       string         `json:"device_id,omitempty"`
	SatelliteID    string         `json:"satellite_id,omitempty"`
	AreaID         string         `json:"area_id,omitempty"`
	Context        map[string]any `json:"context,omitempty"`
	Transcript     any            `json:"transcript,omitempty"`
}

// Execute validates the component's wire shape and carries Assist context in
// metadata so an agent may replace the payload without losing the session.
func (AssistNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if inputs["response"] != nil {
		return executeAssistResponse(ctx, ec, inputs)
	}
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}

	var cfg assistConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if msg.Meta["source"] == "test" || msg.Payload == nil {
		text := strings.TrimSpace(cfg.TestText)
		if text == "" {
			text = "What time is it?"
		}
		msg = &engine.Msg{
			ID: msg.ID,
			Payload: map[string]any{
				"agent_id": cfg.AgentID, "text": text,
				"conversation_id": "karakuri-test", "language": "en",
				"transcript": []any{map[string]any{"role": "user", "text": text}},
			},
			Meta: nodeutil.CloneMeta(msg.Meta),
		}
	}

	req, err := decodeAssistRequest(msg.Payload)
	if err != nil {
		return nil, err
	}
	out := &engine.Msg{ID: msg.ID, Payload: msg.Payload, Topic: msg.Topic, Meta: nodeutil.CloneMeta(msg.Meta), Group: msg.Group}
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	out.Meta["source"] = "ha_assist"
	out.Meta["connection_id"] = cfg.Connection
	if req.RequestID != "" {
		out.Meta[engine.RequestIDMeta] = req.RequestID
	}
	out.Meta["ha_assist"] = map[string]any{
		"agent_id":        req.AgentID,
		"conversation_id": req.ConversationID,
		"language":        req.Language,
		"device_id":       req.DeviceID,
		"satellite_id":    req.SatelliteID,
		"area_id":         req.AreaID,
	}
	return engine.One("out", out), nil
}

func decodeAssistRequest(payload any) (*AssistRequest, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding ha assist request: %w", err)
	}
	var req AssistRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("reading ha assist request: %w", err)
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("ha assist request has no text")
	}
	return &req, nil
}

// Start subscribes to Assist request events until the flow is disabled or
// redeployed. Home Assistant accepts the reply through its respond service.
func (AssistNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg assistConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.AgentID) == "" {
		cfg.AgentID = "default"
	}
	c, ok := waitForConn(ctx, ec, cfg.Connection)
	if !ok {
		return nil
	}
	unsubscribe, err := c.SubscribeEvents(assistRequestEvent, func(event json.RawMessage) {
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(event, &envelope); err != nil {
			ec.Logger().Warn("ha-assist dropped an undecodable request", zap.Error(err))
			return
		}
		req, err := decodeAssistRequest(envelope.Data)
		if err != nil {
			ec.Logger().Warn("ha-assist dropped an invalid request", zap.Error(err))
			return
		}
		if req.RequestID == "" {
			ec.Logger().Warn("ha-assist dropped a request without an id")
			return
		}
		if req.AgentID != cfg.AgentID {
			return
		}
		emit(&engine.Msg{
			Payload: envelope.Data,
			Meta: map[string]any{
				engine.RequestIDMeta: req.RequestID,
				"connection_id":      cfg.Connection,
				"source":             "ha_assist",
			},
		})
	})
	if err != nil {
		return fmt.Errorf("subscribing to Home Assistant Assist requests: %w", err)
	}
	ec.Logger().Info("ha-assist trigger armed", zap.String("agent_id", cfg.AgentID))
	<-ctx.Done()
	unsubscribe()
	return nil
}

type assistReply struct {
	Response             string `json:"response"`
	ConversationID       string `json:"conversation_id,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	Transcript           any    `json:"transcript,omitempty"`
	ContinueConversation *bool  `json:"continue_conversation,omitempty"`
}

// executeAssistResponse sends the result wired back to the trigger through
// the Home Assistant connection that carried the request.
func executeAssistResponse(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg assistConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	responseMsg := inputs["response"]
	transcriptMsg := inputs["transcript"]
	if responseMsg != nil {
		if event, ok := assistStreamPayload(responseMsg.Payload); ok {
			return executeAssistStream(ctx, ec, responseMsg, event)
		}
		if _, ok := responseMsg.Payload.(string); !ok && transcriptMsg == nil &&
			!(cfg.ContinueConversation == "ai" && isAssistAIReply(responseMsg.Payload)) {
			transcriptMsg = responseMsg
		}
	}
	requestID := ""
	if responseMsg != nil {
		requestID = nestedMetaString(responseMsg.Meta, engine.RequestIDMeta)
	}
	if requestID == "" && transcriptMsg != nil {
		requestID = nestedMetaString(transcriptMsg.Meta, engine.RequestIDMeta)
	}
	var aiChoice *bool
	if cfg.ContinueConversation == "ai" {
		choiceMsg := responseMsg
		if choiceMsg == nil {
			choiceMsg = transcriptMsg
		}
		if choiceMsg == nil {
			return nil, fmt.Errorf("ha assist AI decision has no response")
		}
		answer, choice, err := assistAIReply(choiceMsg.Payload)
		if err != nil {
			return nil, err
		}
		aiChoice = &choice
		responseMsg = &engine.Msg{
			Payload: answer, Topic: choiceMsg.Topic, Meta: choiceMsg.Meta, Group: choiceMsg.Group,
		}
	} else if responseMsg == nil {
		if requestID == "" {
			ec.Debug(transcriptMsg.Payload, "no Home Assistant request to answer; Test Run does not send a response")
			return nil, nil
		}
		answer, err := assistTranscriptAnswer(transcriptMsg.Payload)
		if err != nil {
			return nil, err
		}
		responseMsg = &engine.Msg{Payload: answer, Meta: transcriptMsg.Meta}
	}
	answer, ok := responseMsg.Payload.(string)
	if !ok {
		var err error
		answer, err = assistTranscriptAnswer(responseMsg.Payload)
		if err != nil {
			return nil, err
		}
		responseMsg = &engine.Msg{
			Payload: answer, Topic: responseMsg.Topic, Meta: responseMsg.Meta, Group: responseMsg.Group,
		}
	}

	reply := assistReply{Response: answer}
	if transcriptMsg != nil {
		reply.Transcript = transcriptMsg.Payload
		if envelope, ok := transcriptMsg.Payload.(map[string]any); ok {
			reply.SessionID, _ = envelope["session_id"].(string)
		}
	}
	reply.ConversationID = nestedString(responseMsg.Meta, "ha_assist", "conversation_id")
	if reply.SessionID == "" {
		reply.SessionID = nestedString(responseMsg.Meta, "hermes", "session_id")
	}
	switch cfg.ContinueConversation {
	case "ai":
		reply.ContinueConversation = aiChoice
	case "yes":
		value := true
		reply.ContinueConversation = &value
	case "no":
		value := false
		reply.ContinueConversation = &value
	}
	body, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("encoding ha assist response: %w", err)
	}

	if requestID == "" {
		ec.Debug(json.RawMessage(body), "no Home Assistant request to answer; run the flow from Assist to test the reply")
		return nil, nil
	}
	connectionID := nestedMetaString(responseMsg.Meta, "connection_id")
	if connectionID == "" && transcriptMsg != nil {
		connectionID = nestedMetaString(transcriptMsg.Meta, "connection_id")
	}
	if connectionID == "" {
		return nil, fmt.Errorf("ha assist response has no Home Assistant connection")
	}
	c, err := resolveConn(ec, connectionID)
	if err != nil {
		return nil, err
	}
	var serviceData map[string]any
	if err := json.Unmarshal(body, &serviceData); err != nil {
		return nil, fmt.Errorf("reading ha assist response: %w", err)
	}
	serviceData["request_id"] = requestID
	serviceData["agent_id"] = nestedString(responseMsg.Meta, "ha_assist", "agent_id")
	if _, err := c.CallService(ctx, assistServiceDomain, assistRespondService, serviceData, nil); err != nil {
		return nil, fmt.Errorf("sending Home Assistant Assist response: %w", err)
	}
	return nil, nil
}

type assistStreamEvent struct {
	Type       string `json:"type"`
	Index      int    `json:"index"`
	Delta      string `json:"delta"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Output     any    `json:"output"`
	Response   any    `json:"response"`
}

type assistStreamItem struct {
	msg   *engine.Msg
	event assistStreamEvent
}

type assistStreamState struct {
	mu       sync.Mutex
	next     int
	draining bool
	pending  map[int]assistStreamItem
}

func assistStreamPayload(payload any) (assistStreamEvent, bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return assistStreamEvent{}, false
	}
	var event assistStreamEvent
	if json.Unmarshal(raw, &event) != nil {
		return assistStreamEvent{}, false
	}
	switch event.Type {
	case "content.delta", "tool.progress", "context.compacted", "response.completed":
		return event, true
	default:
		return assistStreamEvent{}, false
	}
}

func executeAssistStream(ctx context.Context, ec engine.ExecContext, msg *engine.Msg, event assistStreamEvent) (map[string][]*engine.Msg, error) {
	switch event.Type {
	case "content.delta":
	case "tool.progress":
	case "context.compacted":
	case "response.completed":
	default:
		return nil, fmt.Errorf("ha assist stream event has unknown type %q", event.Type)
	}
	requestID := nestedMetaString(msg.Meta, engine.RequestIDMeta)
	if requestID == "" {
		ec.Debug(msg.Payload, "no Home Assistant request to stream; Test Run does not send a response")
		return nil, nil
	}
	if event.Index < 0 {
		return nil, fmt.Errorf("ha assist stream event has invalid index %d", event.Index)
	}
	key := "assist-stream:" + requestID
	rawState := ec.NodeStore().Update(key, func(current any, ok bool) any {
		if ok {
			return current
		}
		return &assistStreamState{pending: make(map[int]assistStreamItem)}
	})
	state, ok := rawState.(*assistStreamState)
	if !ok {
		return nil, fmt.Errorf("ha assist stream state is invalid")
	}
	return state.enqueue(ctx, ec, key, assistStreamItem{msg: msg, event: event})
}

func (s *assistStreamState) enqueue(ctx context.Context, ec engine.ExecContext, key string, item assistStreamItem) (map[string][]*engine.Msg, error) {
	s.mu.Lock()
	if item.event.Index < s.next {
		s.mu.Unlock()
		return nil, fmt.Errorf("ha assist stream event index %d was already handled", item.event.Index)
	}
	if _, exists := s.pending[item.event.Index]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("ha assist stream event index %d is duplicated", item.event.Index)
	}
	s.pending[item.event.Index] = item
	if s.draining {
		s.mu.Unlock()
		return nil, nil
	}
	s.draining = true
	s.mu.Unlock()

	for {
		s.mu.Lock()
		item, ok := s.pending[s.next]
		if !ok {
			s.draining = false
			s.mu.Unlock()
			return nil, nil
		}
		delete(s.pending, s.next)
		s.next++
		s.mu.Unlock()

		outputs, err := sendAssistStreamEvent(ctx, ec, item.msg, item.event)
		if err != nil {
			ec.NodeStore().Delete(key)
			return nil, err
		}
		if item.event.Type == "response.completed" {
			ec.NodeStore().Delete(key)
			return outputs, nil
		}
	}
}

func sendAssistStreamEvent(ctx context.Context, ec engine.ExecContext, msg *engine.Msg, event assistStreamEvent) (map[string][]*engine.Msg, error) {
	if event.Type == "response.completed" {
		return executeAssistResponse(ctx, ec, map[string]*engine.Msg{
			"response":   {Payload: event.Output, Topic: msg.Topic, Meta: msg.Meta, Group: msg.Group},
			"transcript": {Payload: event.Response, Topic: msg.Topic, Meta: msg.Meta, Group: msg.Group},
		})
	}
	if event.Type == "tool.progress" {
		return sendAssistToolProgress(ctx, ec, msg, event)
	}
	// A compaction notice takes a stream index like any other event, so it is
	// queued to keep the order, but Home Assistant has nothing to show for it.
	if event.Type == "context.compacted" {
		return nil, nil
	}
	var cfg assistConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.ContinueConversation == "ai" {
		return nil, nil
	}
	if event.Delta == "" {
		return nil, nil
	}
	requestID := nestedMetaString(msg.Meta, engine.RequestIDMeta)
	connectionID := nestedMetaString(msg.Meta, "connection_id")
	if connectionID == "" {
		return nil, fmt.Errorf("ha assist stream has no Home Assistant connection")
	}
	c, err := resolveConn(ec, connectionID)
	if err != nil {
		return nil, err
	}
	serviceData := map[string]any{
		"request_id": requestID,
		"agent_id":   nestedString(msg.Meta, "ha_assist", "agent_id"),
		"response":   event.Delta,
		"stream":     true,
	}
	if _, err := c.CallService(ctx, assistServiceDomain, assistRespondService, serviceData, nil); err != nil {
		return nil, fmt.Errorf("sending Home Assistant Assist stream: %w", err)
	}
	return nil, nil
}

func sendAssistToolProgress(ctx context.Context, ec engine.ExecContext, msg *engine.Msg, event assistStreamEvent) (map[string][]*engine.Msg, error) {
	if event.ToolCallID == "" || event.ToolName == "" || event.Status == "" {
		return nil, nil
	}
	connectionID := nestedMetaString(msg.Meta, "connection_id")
	if connectionID == "" {
		return nil, fmt.Errorf("ha assist stream has no Home Assistant connection")
	}
	c, err := resolveConn(ec, connectionID)
	if err != nil {
		return nil, err
	}
	serviceData := map[string]any{
		"request_id":   nestedMetaString(msg.Meta, engine.RequestIDMeta),
		"agent_id":     nestedString(msg.Meta, "ha_assist", "agent_id"),
		"response":     "",
		"stream":       true,
		"tool_call_id": event.ToolCallID,
		"tool_name":    event.ToolName,
		"tool_label":   event.Label,
		"tool_status":  event.Status,
	}
	if _, err := c.CallService(ctx, assistServiceDomain, assistRespondService, serviceData, nil); err != nil {
		return nil, fmt.Errorf("sending Home Assistant Assist tool progress: %w", err)
	}
	return nil, nil
}

// assistTranscriptAnswer reads the final assistant turn from an AI Agent or
// AI Basic Chain transcript. Provider-backed histories use content while
// Karakuri's normalized entries use text.
func assistTranscriptAnswer(payload any) (string, error) {
	transcript := payload
	if envelope, ok := payload.(map[string]any); ok {
		transcript = envelope["transcript"]
	}
	raw, err := json.Marshal(transcript)
	if err != nil {
		return "", fmt.Errorf("encoding ha assist transcript: %w", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", fmt.Errorf("ha assist transcript must contain an entry array: %w", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["role"] != "assistant" {
			continue
		}
		for _, field := range []string{"text", "content"} {
			if answer, ok := entries[i][field].(string); ok && strings.TrimSpace(answer) != "" {
				return answer, nil
			}
		}
	}
	return "", fmt.Errorf("ha assist transcript has no assistant response")
}

// assistAIReply reads the spoken answer and follow-up choice from the JSON
// object returned by an AI node. A transcript envelope carries the same JSON
// as its final assistant turn so provider replay data can remain intact.
func assistAIReply(payload any) (string, bool, error) {
	if envelope, ok := payload.(map[string]any); ok {
		if _, hasResponse := envelope["response"]; hasResponse {
			answer, answerOK := envelope["response"].(string)
			choice, choiceOK := envelope["continue_conversation"].(bool)
			if !answerOK || strings.TrimSpace(answer) == "" || !choiceOK {
				return "", false, fmt.Errorf("ha assist AI response must contain response text and a continue_conversation boolean")
			}
			return answer, choice, nil
		}
	}

	rawReply, ok := payload.(string)
	if !ok {
		var err error
		rawReply, err = assistTranscriptAnswer(payload)
		if err != nil {
			return "", false, err
		}
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(rawReply), &reply); err != nil {
		return "", false, fmt.Errorf("ha assist AI response is not valid JSON: %w", err)
	}
	return assistAIReply(reply)
}

func isAssistAIReply(payload any) bool {
	reply, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	_, hasResponse := reply["response"]
	return hasResponse
}

func nestedMetaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return value
}

func nestedString(meta map[string]any, object, key string) string {
	if meta == nil {
		return ""
	}
	nested, _ := meta[object].(map[string]any)
	value, _ := nested[key].(string)
	return value
}
