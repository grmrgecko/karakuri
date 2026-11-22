package ainodes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// SanitizeNode redacts sensitive spans from text before it reaches a model, a
// log, or a third party: PII and secret keys by default, URLs, keywords, and
// custom patterns on top. It shares the guardrail node's detectors, so what
// one flags the other removes; unlike the guardrail it never asks a model,
// which is what makes it safe in front of one.
type SanitizeNode struct{}

// Meta describes the ai-sanitize node type.
func (SanitizeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ai-sanitize",
		Label:       "AI Sanitize",
		Folder:      flow.FolderAI,
		Description: "Redacts PII, secrets, URLs, and custom patterns from text",
		Icon:        "las la-eraser",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "text", Label: "Text", Type: flow.FieldText,
				Placeholder: "{{ payload.prompt }}",
				Help:        "Text to sanitize. Supports {{ }} expression segments; empty sanitizes the payload as text.",
			},
			{
				ID: "pii", Label: "PII", Type: flow.FieldBoolean, Default: true,
				Help: "Replace personally identifiable information with its type, [email] for an address.",
			},
			{
				ID: "pii_types", Label: "PII types", Type: flow.FieldString, Control: "tokens",
				ShowIf:  &flow.ShowIf{Field: "pii", Equals: true},
				Options: piiTypeOptions(),
				Help:    "Types to redact. Empty redacts all of them.",
			},
			{
				ID: "secrets", Label: "Secret keys", Type: flow.FieldBoolean, Default: true,
				Help: "Replace API keys, access tokens, private keys, and credential assignments with [secret].",
			},
			{
				ID: "secrets_level", Label: "Secrets sensitivity", Type: flow.FieldSelect,
				ShowIf:  &flow.ShowIf{Field: "secrets", Equals: true},
				Default: "balanced",
				Options: []flow.Option{
					{Value: "strict", Label: "Strict"},
					{Value: "balanced", Label: "Balanced"},
					{Value: "permissive", Label: "Permissive"},
				},
				Help: "How aggressively random-looking tokens are treated as secrets, beyond the known key formats. Strict flags shorter and less random strings; permissive only long, high-entropy ones.",
			},
			{
				ID: "urls", Label: "URLs", Type: flow.FieldBoolean,
				Help: "Replace every URL with [url].",
			},
			{
				ID: "keywords", Label: "Keywords", Type: flow.FieldString, Control: "tokens",
				Placeholder: "confidential",
				Help:        "Keywords to replace with [redacted], matched case-insensitively. Empty redacts none.",
			},
			{
				ID: "patterns", Label: "Patterns", Type: flow.FieldText,
				Placeholder: `(?i)ticket-[0-9]+`,
				Help:        "Go regular expressions to replace with [redacted], one per line. Empty redacts none.",
			},
		},
	}
}

// sanitizeConfig is the decoded ai-sanitize node configuration. The two
// on-by-default toggles are pointers so an omitted field stays on and an
// explicit false turns it off.
type sanitizeConfig struct {
	Text         string `json:"text"`
	PII          *bool  `json:"pii"`
	PIITypes     string `json:"pii_types"`
	Secrets      *bool  `json:"secrets"`
	SecretsLevel string `json:"secrets_level"`
	URLs         bool   `json:"urls"`
	Keywords     string `json:"keywords"`
	Patterns     string `json:"patterns"`
}

// Execute renders the text, collects the spans the enabled detectors flag,
// and emits the text with each span replaced by its placeholder. The
// redaction counts ride meta.sanitized so a flow can see what was removed
// without seeing what it was.
func (SanitizeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg sanitizeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	text, err := checkedText(ec, cfg.Text, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering text: %w", err)
	}

	// Collect spans in priority order: the author's own patterns and keywords
	// outrank the built-in detectors, and secrets outrank PII so a token
	// holding digits reads [secret] rather than [phone]. The order only breaks
	// ties: overlapping spans are joined below, so no detector's match shields
	// part of another's from redaction.
	var found []detection
	patterns, err := compilePatterns(cfg.Patterns)
	if err != nil {
		return nil, err
	}
	found = append(found, detectPatterns(text, patterns)...)
	found = append(found, detectKeywords(text, parseList(cfg.Keywords))...)
	if cfg.Secrets == nil || *cfg.Secrets {
		secrets, err := detectSecrets(text, cfg.SecretsLevel)
		if err != nil {
			return nil, err
		}
		found = append(found, secrets...)
	}
	if cfg.PII == nil || *cfg.PII {
		types, err := piiTypeSet(cfg.PIITypes)
		if err != nil {
			return nil, err
		}
		found = append(found, detectPII(text, types)...)
	}
	if cfg.URLs {
		found = append(found, findURLs(text)...)
	}

	// Rebuild the text with each span replaced by its placeholder.
	var b strings.Builder
	counts := map[string]int{}
	last := 0
	for _, d := range coverDetections(found) {
		b.WriteString(text[last:d.start])
		b.WriteString("[" + d.label + "]")
		counts[d.kind]++
		last = d.end
	}
	b.WriteString(text[last:])

	meta := nodeutil.CloneMeta(msg.Meta)
	// The counts are stored as a JSON object, which is the only shape a
	// property path such as meta.sanitized.email walks.
	sanitized := make(map[string]any, len(counts))
	for kind, n := range counts {
		sanitized[kind] = n
	}
	meta["sanitized"] = sanitized
	return engine.One("out", &engine.Msg{Payload: b.String(), Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}

// coverDetections joins overlapping spans into one span covering all of them,
// so every character a detector flagged is redacted. Keeping only the first
// span to claim a region left the rest of a wider one in the text: a keyword
// in front of a password, or an address inside a URL. The joined span takes
// the kind and label of its widest member, the earliest in found when two are
// equally wide, and found is in priority order. The result is sorted by start
// and free of overlaps.
func coverDetections(found []detection) []detection {
	if len(found) == 0 {
		return nil
	}
	order := make([]int, len(found))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return found[order[i]].start < found[order[j]].start })

	covered := make([]detection, 0, len(found))
	// widest is the index in found of the member the current span is named
	// after.
	widest := 0
	for _, idx := range order {
		d := found[idx]
		if len(covered) == 0 || d.start >= covered[len(covered)-1].end {
			covered = append(covered, d)
			widest = idx
			continue
		}
		cur := &covered[len(covered)-1]
		if d.end > cur.end {
			cur.end = d.end
		}
		width, best := d.end-d.start, found[widest].end-found[widest].start
		if width > best || (width == best && idx < widest) {
			cur.kind, cur.label = d.kind, d.label
			widest = idx
		}
	}
	return covered
}
