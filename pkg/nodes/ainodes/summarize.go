package ainodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
)

const (
	defaultSummaryChunkSize = 12000
	defaultSummaryOverlap   = 500
	maxSummaryCalls         = 64
	maxSummaryRounds        = 8
)

// SummarizeNode maps document chunks to summaries, then reduces those
// summaries until one remains.
type SummarizeNode struct{}

// Meta describes the ai-summarize node type.
func (SummarizeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "ai-summarize", Label: "AI Summarize", Folder: flow.FolderAI,
		Description: "Summarizes long text with bounded map-reduce model calls",
		Icon:        "las la-file-medical-alt", Retryable: true,
		Inputs:  []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortString}, flow.ErrorPort()},
		ConfigSpec: []engine.FieldSpec{
			{ID: "model", Label: "Model", Type: flow.FieldJSON, Port: PortAIModel, Required: true},
			{ID: "text", Label: "Text", Type: flow.FieldText, Help: "Text to summarize. Empty reads the payload as text."},
			{ID: "instructions", Label: "Instructions", Type: flow.FieldText, Help: "What the summary should retain. Supports {{ }} expression segments."},
			{ID: "chunk_size", Label: "Chunk size", Type: flow.FieldNumber, Default: defaultSummaryChunkSize, Help: "Maximum bytes sent as source text in one map or reduce call."},
		},
	}
}

type summarizeConfig struct {
	Model        modelHandle `json:"model"`
	Text         string      `json:"text"`
	Instructions string      `json:"instructions"`
	ChunkSize    float64     `json:"chunk_size"`
}

// Execute summarizes the configured text without parallel model requests.
func (SummarizeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg summarizeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	if cfg.Model.Connection == "" || cfg.Model.Model == "" {
		return nil, fmt.Errorf("ai-summarize node has no model wired")
	}
	text, err := checkedText(ec, cfg.Text, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering text: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("ai-summarize node has no text to summarize")
	}
	instructions, err := ec.Interpolate(cfg.Instructions, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering instructions: %w", err)
	}
	chunkSize := int(cfg.ChunkSize)
	if chunkSize <= 0 {
		chunkSize = defaultSummaryChunkSize
	}
	chunks, err := chunkText(ctx, text, chunkSize, min(defaultSummaryOverlap, chunkSize/10))
	if err != nil {
		return nil, err
	}
	if len(chunks) > maxSummaryCalls {
		return nil, fmt.Errorf("summary needs %d initial calls; the limit is %d", len(chunks), maxSummaryCalls)
	}
	conn, err := resolveChatConn(ec, cfg.Model.Connection)
	if err != nil {
		return nil, err
	}

	system := "Summarize the supplied text faithfully. Keep names, decisions, constraints, dates, and unresolved questions. Do not add facts. Return only the summary."
	if strings.TrimSpace(instructions) != "" {
		system += "\n\n" + instructions
	}
	var usage ai.Usage
	calls := 0
	rounds := 1
	summaries, err := summarizeChunks(ctx, conn, cfg.Model, system, chunks, &usage, &calls)
	if err != nil {
		return nil, err
	}
	for len(summaries) > 1 {
		if rounds >= maxSummaryRounds {
			return nil, fmt.Errorf("summary did not reduce to one result within %d rounds", maxSummaryRounds)
		}
		groups, err := chunkText(ctx, strings.Join(summaries, "\n\n"), chunkSize, 0)
		if err != nil {
			return nil, err
		}
		if calls+len(groups) > maxSummaryCalls {
			return nil, fmt.Errorf("summary exceeded the %d call limit", maxSummaryCalls)
		}
		summaries, err = summarizeChunks(ctx, conn, cfg.Model, system+"\n\nCombine the partial summaries into one coherent summary. Remove repetition.", groups, &usage, &calls)
		if err != nil {
			return nil, err
		}
		rounds++
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	meta["model"] = cfg.Model.Model
	meta["usage"] = jsonShaped(usage)
	meta["summarize"] = map[string]any{"chunks": len(chunks), "calls": calls, "rounds": rounds}
	return engine.One("out", &engine.Msg{Payload: summaries[0], Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}

func summarizeChunks(ctx context.Context, conn chatConn, model modelHandle, system string, chunks []string, usage *ai.Usage, calls *int) ([]string, error) {
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		req := ai.ChatRequest{System: system, Messages: []ai.Message{{Role: ai.RoleUser, Text: chunk}}}
		applyModel(&req, model)
		resp, err := conn.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		(*calls)++
		addUsage(usage, resp.Usage)
		summary := strings.TrimSpace(resp.Text)
		if summary == "" {
			return nil, fmt.Errorf("model returned an empty summary")
		}
		out = append(out, summary)
	}
	return out, nil
}
