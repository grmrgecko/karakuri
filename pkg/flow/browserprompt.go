package flow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// NodeTypeBrowserPrompt identifies the server-owned human question node.
const NodeTypeBrowserPrompt = "browser-prompt"

const (
	// MaxAttachmentBytes bounds one decoded file.
	MaxAttachmentBytes = 8 << 20
	// MaxAttachmentTotal bounds a node's decoded files.
	MaxAttachmentTotal = 32 << 20
	// MaxAttachments bounds the number of files stored on a node.
	MaxAttachments = 20
	// MaxPromptFeedback bounds an answer's UTF-8 bytes.
	MaxPromptFeedback = 4096
)

var promptKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// PromptButton names one response and its graph output.
type PromptButton struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Attachment travels with a graph and its frozen versions.
type Attachment struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Data     string `json:"data"`
}

// BrowserPromptConfig describes one human question.
type BrowserPromptConfig struct {
	Agent           string                `json:"agent"`
	BrowserBaseURL  string                `json:"browser_base_url"`
	Title           string                `json:"title"`
	Markdown        string                `json:"markdown"`
	Buttons         []PromptButton        `json:"buttons"`
	Attachments     map[string]Attachment `json:"attachments"`
	Timeout         string                `json:"timeout"`
	Feedback        bool                  `json:"feedback"`
	RequireFeedback bool                  `json:"require_feedback"`
}

// DefaultPromptButtons supplies the initial choices for an unconfigured node.
func DefaultPromptButtons() []PromptButton {
	return []PromptButton{{ID: "retry", Label: "Retry"}, {ID: "bypass", Label: "Bypass"}, {ID: "stop", Label: "Stop"}}
}

// PromptButtonsSchema describes choice records to catalog and MCP consumers.
func PromptButtonsSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 1, "maxItems": 12,
		"items": map[string]any{
			"type": "object", "required": []string{"id", "label"},
			"properties": map[string]any{
				"id":    map[string]any{"type": "string", "pattern": promptKeyPattern.String(), "description": "Unique stable output ID; error is reserved."},
				"label": map[string]any{"type": "string", "minLength": 1, "description": "Button text, at most 200 UTF-8 bytes."},
			},
		},
	}
}

// PromptAttachmentsSchema describes files that travel with saved graphs.
func PromptAttachmentsSchema() map[string]any {
	return map[string]any{
		"type": "object", "maxProperties": MaxAttachments,
		"propertyNames": map[string]any{"pattern": promptKeyPattern.String()},
		"additionalProperties": map[string]any{
			"type": "object", "required": []string{"filename", "type", "data"},
			"properties": map[string]any{
				"filename": map[string]any{"type": "string", "description": "Download filename, 1-255 UTF-8 bytes, without separators or control characters."},
				"type":     map[string]any{"type": "string", "description": "MIME type; verified PNG, JPEG, GIF and WebP can embed."},
				"data":     map[string]any{"type": "string", "contentEncoding": "base64", "description": "Strict base64; 8 MiB decoded per file, 32 MiB total."},
			},
		},
	}
}

// PromptButtons preserves an explicitly empty list for draft editing.
func PromptButtons(raw json.RawMessage) []PromptButton {
	var cfg BrowserPromptConfig
	if len(raw) != 0 && json.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	if cfg.Buttons == nil {
		return DefaultPromptButtons()
	}
	return cfg.Buttons
}

// BrowserPromptPorts derives stable choice outputs above the error output.
func BrowserPromptPorts(raw json.RawMessage) []PortSpec {
	ports := []PortSpec{}
	for _, button := range PromptButtons(raw) {
		ports = append(ports, PortSpec{ID: button.ID, Label: button.Label, Type: PortAny, TypeFrom: "in"})
	}
	return append(ports, ErrorPort())
}

// PromptTimeout bounds how long a question remains answerable.
func PromptTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return 15 * time.Minute, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second || d > 24*time.Hour {
		return 0, fmt.Errorf("answer timeout must be a duration from 1s through 24h")
	}
	return d, nil
}

// ValidatePromptButtons rejects ambiguous or reserved output IDs.
func ValidatePromptButtons(buttons []PromptButton, lenient bool) []string {
	var problems []string
	if len(buttons) > 12 || (!lenient && len(buttons) == 0) {
		problems = append(problems, "buttons must contain 1 through 12 choices")
	}
	seen := map[string]bool{}
	for _, b := range buttons {
		if !promptKeyPattern.MatchString(b.ID) || b.ID == "error" {
			problems = append(problems, fmt.Sprintf("button id %q must be 1-64 letters, digits, dot, dash or underscore and cannot be error", b.ID))
		}
		if seen[b.ID] {
			problems = append(problems, fmt.Sprintf("button id %q is duplicated", b.ID))
		}
		seen[b.ID] = true
		if (!lenient && strings.TrimSpace(b.Label) == "") || len(b.Label) > 200 {
			problems = append(problems, fmt.Sprintf("button %q needs a label of at most 200 bytes", b.ID))
		}
	}
	return problems
}

// AttachmentBytes validates portable data before serving it to a browser.
func AttachmentBytes(a Attachment) ([]byte, error) {
	if a.Filename == "" || len(a.Filename) > 255 || strings.ContainsAny(a.Filename, `/\`) || strings.IndexFunc(a.Filename, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("attachment filename must be 1-255 bytes without separators or control characters")
	}
	if len(a.Data) > base64.StdEncoding.EncodedLen(MaxAttachmentBytes) {
		return nil, fmt.Errorf("attachment exceeds 8 MiB")
	}
	b, err := base64.StdEncoding.Strict().DecodeString(a.Data)
	if err != nil || base64.StdEncoding.EncodeToString(b) != a.Data {
		return nil, fmt.Errorf("attachment data must be strict base64")
	}
	if len(b) > MaxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds 8 MiB")
	}
	if len(a.Type) > 127 || strings.ContainsAny(a.Type, "\r\n") {
		return nil, fmt.Errorf("attachment MIME type is invalid")
	}
	if IsPromptImage(a.Type) && http.DetectContentType(b) != a.Type {
		return nil, fmt.Errorf("attachment image content does not match %s", a.Type)
	}
	return b, nil
}

// IsPromptImage reports the raster formats allowed inline in instructions.
func IsPromptImage(kind string) bool {
	return kind == "image/png" || kind == "image/jpeg" || kind == "image/gif" || kind == "image/webp"
}

// ValidateAttachments applies the same security checks to drafts and executions.
func ValidateAttachments(attachments map[string]Attachment) []string {
	var problems []string
	if len(attachments) > MaxAttachments {
		return []string{"attachments holds more than 20 files"}
	}
	total := 0
	for key, a := range attachments {
		if !promptKeyPattern.MatchString(key) {
			problems = append(problems, fmt.Sprintf("attachment key %q is invalid", key))
			continue
		}
		b, err := AttachmentBytes(a)
		if err != nil {
			problems = append(problems, fmt.Sprintf("attachment %q: %s", key, err))
			continue
		}
		total += len(b)
	}
	if total > MaxAttachmentTotal {
		problems = append(problems, "attachments exceed 32 MiB in total")
	}
	return problems
}

// PromptBaseURL requires the server origin because the prompt API uses root paths.
func PromptBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("browser base URL must be an http or https server origin without a path, credentials, query or fragment")
	}
	return u, nil
}

func validateBrowserPrompt(p *problems, key string, n *Node, lenient bool) {
	var cfg BrowserPromptConfig
	if err := json.Unmarshal(n.Config, &cfg); len(n.Config) != 0 && err != nil {
		p.add(key, "browser prompt config is invalid: %s", err)
		return
	}
	if cfg.Buttons == nil {
		cfg.Buttons = DefaultPromptButtons()
	}
	for _, param := range n.Params {
		switch param {
		case "timeout":
			cfg.Timeout = ""
		case "browser_base_url":
			cfg.BrowserBaseURL = ""
		}
	}
	for _, problem := range ValidatePromptButtons(cfg.Buttons, lenient) {
		p.add(key, "%s", problem)
	}
	if _, err := PromptTimeout(cfg.Timeout); err != nil {
		p.add(key, "%s", err)
	}
	if cfg.BrowserBaseURL != "" {
		if _, err := PromptBaseURL(cfg.BrowserBaseURL); err != nil {
			p.add(key, "%s", err)
		}
	}
}
