package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxResponseBytes caps how much of a provider response is read, matching the
// http-request node's ceiling. A chat completion is text; anything past this
// is a misbehaving endpoint, not an answer.
const maxResponseBytes = 8 << 20

// httpClient is shared by every wire. No client timeout: a large local model
// legitimately takes minutes, and the node's context is the actual bound.
var httpClient = &http.Client{}

type capturedSSEEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// doSSE sends a JSON request and reads its server-sent events. The returned
// JSON array retains event names, including provider-specific progress events.
func doSSE(ctx context.Context, url string, headers map[string]string, body any, handle func(string, json.RawMessage) error) (http.Header, json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		if len(raw) > maxResponseBytes {
			return nil, nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
		}
		if message := apiErrorMessage(raw); message != "" {
			return nil, nil, fmt.Errorf("%s: %s", resp.Status, message)
		}
		return nil, nil, fmt.Errorf("%s", resp.Status)
	}

	var captures []capturedSSEEvent
	var eventName string
	var dataLines []string
	total := 0
	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		name := eventName
		eventName = ""
		if name == "" {
			name = "message"
		}
		var value any
		if json.Unmarshal([]byte(data), &value) != nil {
			value = data
		}
		captures = append(captures, capturedSSEEvent{Event: name, Data: value})
		if data == "[DONE]" {
			return nil
		}
		return handle(name, json.RawMessage(data))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), maxResponseBytes)
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxResponseBytes {
			return nil, nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
		}
		if line == "" {
			if err := flush(); err != nil {
				return nil, nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading stream: %w", err)
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(captures)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding stream capture: %w", err)
	}
	return resp.Header.Clone(), raw, nil
}

// doJSON sends one JSON request and decodes the JSON reply into out. A non-2xx
// answer becomes an error carrying the provider's own message when one can be
// dug out of the body, because "401" alone sends an operator to the wrong
// place — the message names the bad key or the missing model.
func doJSON(ctx context.Context, method, url string, headers map[string]string, body any, out any) error {
	_, err := doJSONRaw(ctx, method, url, headers, body, out)
	return err
}

// doJSONRaw sends one JSON request, decodes the reply, and returns the exact
// successful response body.
func doJSONRaw(ctx context.Context, method, url string, headers map[string]string, body any, out any) (json.RawMessage, error) {
	raw, _, err := doJSONRawHeaders(ctx, method, url, headers, body, out)
	return raw, err
}

// doJSONRawHeaders also returns the response headers used by providers whose
// follow-up endpoint is identified outside the JSON body.
func doJSONRawHeaders(ctx context.Context, method, url string, headers map[string]string, body any, out any) (json.RawMessage, http.Header, error) {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if msg := apiErrorMessage(raw); msg != "" {
			return nil, nil, fmt.Errorf("%s: %s", resp.Status, msg)
		}
		return nil, nil, fmt.Errorf("%s", resp.Status)
	}
	if out == nil {
		return append(json.RawMessage(nil), raw...), resp.Header.Clone(), nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, nil, fmt.Errorf("decoding response: %w", err)
	}
	return append(json.RawMessage(nil), raw...), resp.Header.Clone(), nil
}

// apiErrorMessage extracts a human message from a provider error body. The
// three APIs spell it differently — OpenAI and Gemini nest {"error":
// {"message": ...}}, Anthropic {"error": {"message": ...}} too, and some
// compatible servers answer {"error": "..."} or {"message": "..."} — so every
// spelling is tried before giving up.
func apiErrorMessage(raw []byte) string {
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &nested) == nil && nested.Error.Message != "" {
		return nested.Error.Message
	}
	var flat struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &flat) == nil {
		if flat.Error != "" {
			return flat.Error
		}
		if flat.Message != "" {
			return flat.Message
		}
	}
	return ""
}

// streamError returns the failure a provider reported inside a 200 event
// stream. Once the status line is sent a provider can only report a failure
// as an event whose data holds a top-level error, so a stream handler that
// read past it would return the text cut off before the failure as a finished
// answer. The provider's message is kept in the error so overflow detection
// still reads it.
func streamError(data json.RawMessage) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil
	}
	if len(envelope.Error) == 0 || string(envelope.Error) == "null" {
		return nil
	}
	if message := apiErrorMessage(data); message != "" {
		return fmt.Errorf("stream failed: %s", message)
	}
	return fmt.Errorf("stream failed: %s", envelope.Error)
}
