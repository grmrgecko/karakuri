package ainodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
)

func applyModel(req *ai.ChatRequest, model modelHandle) {
	req.Model = model.Model
	req.Temperature = model.Temperature
	req.MaxTokens = model.MaxTokens
	req.ReasoningEffort = model.ReasoningEffort
	req.ReasoningBudget = model.ReasoningBudget
}

func addUsage(total *ai.Usage, usage ai.Usage) {
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.CacheWriteInputTokens += usage.CacheWriteInputTokens
}

func decodeStructured(text string, schema map[string]any) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &value); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	if err := validateJSONValue(value, schema); err != nil {
		return nil, fmt.Errorf("response does not match schema: %w", err)
	}
	return value, nil
}

// repairStructured makes at most one corrective call and returns the visible
// turns inserted before it so an agent transcript can retain the correction.
func repairStructured(ctx context.Context, conn chatConn, req ai.ChatRequest, first *ai.ChatResponse) (any, string, string, map[string]json.RawMessage, string, []ai.Message, ai.Usage, []json.RawMessage, []map[string]any, error) {
	usage := first.Usage
	responses := providerResponses(first)
	transcript := providerTranscript(first)
	value, validationErr := decodeStructured(first.Text, req.JSONSchema)
	if validationErr == nil {
		return value, first.Text, first.ReasoningSummary, first.OpaqueState, first.SessionID, nil, usage, responses, transcript, nil
	}

	turns := []ai.Message{
		{Role: ai.RoleAssistant, Text: first.Text, ReasoningSummary: first.ReasoningSummary, OpaqueState: first.OpaqueState},
		{Role: ai.RoleUser, Text: "Your previous response was rejected with this exact validation error:\n" + validationErr.Error() + "\nReturn one corrected JSON object only. Do not include markdown or explanation."},
	}
	req.Messages = append(req.Messages, turns...)
	if first.SessionID != "" {
		req.SessionID = first.SessionID
	}
	req.Tools = nil
	req.JSONOutput = true
	repaired, err := conn.Chat(ctx, req)
	if err != nil {
		return nil, "", "", nil, "", turns, usage, responses, transcript, err
	}
	responses = append(responses, providerResponses(repaired)...)
	if repairedTranscript := providerTranscript(repaired); len(repairedTranscript) > 0 {
		transcript = repairedTranscript
	}
	addUsage(&usage, repaired.Usage)
	value, err = decodeStructured(repaired.Text, req.JSONSchema)
	if err != nil {
		return nil, repaired.Text, repaired.ReasoningSummary, repaired.OpaqueState, repaired.SessionID, turns, usage, responses, transcript, err
	}
	return value, repaired.Text, repaired.ReasoningSummary, repaired.OpaqueState, repaired.SessionID, turns, usage, responses, transcript, nil
}

func providerResponses(resp *ai.ChatResponse) []json.RawMessage {
	if resp == nil {
		return nil
	}
	responses := make([]json.RawMessage, 0, 1+len(resp.ProviderSupplementalResponses))
	if len(resp.ProviderResponse) > 0 {
		responses = append(responses, append(json.RawMessage(nil), resp.ProviderResponse...))
	}
	for _, raw := range resp.ProviderSupplementalResponses {
		responses = append(responses, append(json.RawMessage(nil), raw...))
	}
	return responses
}

func providerTranscript(resp *ai.ChatResponse) []map[string]any {
	if resp == nil || len(resp.ProviderTranscript) == 0 {
		return nil
	}
	return append([]map[string]any(nil), resp.ProviderTranscript...)
}
