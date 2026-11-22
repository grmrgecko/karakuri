package ainodes

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/memories"
)

// Chunking defaults: sized so a chunk carries a coherent passage without
// spending a recall slot on a whole chapter, with enough overlap that a fact
// straddling a boundary survives in one piece on one side of it.
const (
	defaultChunkSize    = 1200
	defaultChunkOverlap = 150
)

// IngestNode is the retrieval half of RAG's write path: it chunks a document
// into a memory store, where the existing recall machinery — embeddings,
// hybrid scoring, the agent's memory handle — serves it back. Each run
// replaces the source's previous chunks, so re-reading a changed file updates
// the store instead of piling new chunks beside stale ones; ingesting empty
// text is therefore how a deleted document is forgotten.
type IngestNode struct{}

// Meta describes the memory-ingest node type.
func (IngestNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "memory-ingest",
		Label:       "Ingest",
		Folder:      flow.FolderAI,
		Description: "Chunks a document into a memory store, replacing the source's previous chunks",
		Icon:        "las la-file-import",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "store", Label: "Memory store", Type: flow.FieldString,
				Control: "memory-store", Required: true,
				Help: "Memory store the chunks land in. For reference material, a store with recency weight 0 and no fact cap.",
			},
			{
				ID: "source", Label: "Source", Type: flow.FieldString, Required: true,
				Placeholder: "{{ meta.path }}",
				Help:        "Name of the document these chunks came from. Re-ingesting the same source replaces its chunks. Supports {{ }} expression segments.",
			},
			{
				ID: "text", Label: "Text", Type: flow.FieldText,
				Help: "Document text to ingest. Supports {{ }} expression segments; empty reads the payload as text. Ingesting an empty document forgets the source.",
			},
			{
				ID: "chunk_size", Label: "Chunk size", Type: flow.FieldNumber,
				Default: defaultChunkSize,
				Help:    "Longest chunk, in bytes of UTF-8 text. Paragraphs are packed up to it; a longer paragraph is split. A character wider than the size is kept whole.",
			},
			{
				ID: "chunk_overlap", Label: "Chunk overlap", Type: flow.FieldNumber,
				Default: defaultChunkOverlap,
				Help:    "Bytes each chunk repeats from the end of the one before, so a fact on a boundary stays whole somewhere. At most half the chunk size.",
			},
			{
				ID: "tags", Label: "Tags", Type: flow.FieldString, Control: "tokens",
				Help: "Tags on every chunk, matched alongside the text in recall.",
			},
		},
	}
}

// ingestConfig is the decoded memory-ingest node configuration. The chunking
// fields are pointers so an omitted field gets the default and an explicit
// zero overlap means none.
type ingestConfig struct {
	Store        string   `json:"store"`
	Source       string   `json:"source"`
	Text         string   `json:"text"`
	ChunkSize    *float64 `json:"chunk_size"`
	ChunkOverlap *float64 `json:"chunk_overlap"`
	Tags         string   `json:"tags"`
}

// Execute chunks the document and replaces the source's memories.
func (IngestNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg ingestConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	snap, err := resolveMemoryStore(cfg.Store)
	if err != nil {
		return nil, err
	}
	source, err := ec.Interpolate(cfg.Source, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering source: %w", err)
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("memory-ingest node has no source name")
	}
	text, err := checkedText(ec, cfg.Text, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering text: %w", err)
	}

	size := defaultChunkSize
	if cfg.ChunkSize != nil && *cfg.ChunkSize > 0 {
		size = int(*cfg.ChunkSize)
	}
	overlap := defaultChunkOverlap
	if cfg.ChunkOverlap != nil {
		overlap = int(*cfg.ChunkOverlap)
	}
	chunks, err := chunkText(ctx, text, size, overlap)
	if err != nil {
		return nil, err
	}

	// Replace the source's chunks in one transaction, so a re-ingest never
	// doubles the source and a failed write leaves the previous version whole.
	tags := flow.SplitTokens(cfg.Tags)
	params := make([]memories.MemoryParams, len(chunks))
	for i, chunk := range chunks {
		params[i] = memories.MemoryParams{Text: chunk, Tags: tags}
	}
	replaced, _, err := memories.S.ReplaceSource(snap.ID, source, params)
	if err != nil {
		return nil, fmt.Errorf("replacing the source's chunks: %w", err)
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	meta["ingest"] = map[string]any{
		"store": snap.Name, "source": source,
		"chunks": len(chunks), "replaced": replaced,
	}
	return engine.One("out", &engine.Msg{Payload: msg.Payload, Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}

// chunkText splits a document into recall-sized chunks: paragraphs packed up
// to size, a paragraph longer than size split at word boundaries, and each
// chunk after the first opening with the tail of the one before it so a fact
// straddling a boundary survives whole on one side. Sizes are in bytes with
// splits kept off the middle of a rune. Every split advances by at least one
// whole character, so a budget smaller than a character still finishes, and a
// cancelled run stops between splits.
func chunkText(ctx context.Context, text string, size, overlap int) ([]string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil, nil
	}
	if size <= 0 {
		size = defaultChunkSize
	}
	// An overlap at or above the chunk size would never advance.
	if overlap < 0 {
		overlap = 0
	}
	if overlap > size/2 {
		overlap = size / 2
	}
	if len(text) <= size {
		return []string{text}, nil
	}

	// Split into paragraphs, breaking any longer than a chunk at word
	// boundaries first so packing only ever handles pieces that fit.
	// continued marks a piece that already opens with the tail of the piece
	// before it, so packing does not carry that tail a second time.
	var pieces []string
	var continued []bool
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		overlapped := false
		// carried is how many bytes of para repeat the piece before it.
		carried := 0
		for len(para) > size {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cut := splitPoint(para, size, carried)
			pieces = append(pieces, strings.TrimSpace(para[:cut]))
			continued = append(continued, overlapped)
			// The overlap repeats text into the next piece, but never all of
			// this one, or the split would start where it began.
			// The tail is measured on the trimmed piece, so it is counted back
			// from the end of that text, not from a cut that may sit past
			// trailing whitespace.
			head := strings.TrimRightFunc(para[:cut], unicode.IsSpace)
			tail := overlapTailLen(head, overlap)
			next := len(head) - tail
			overlapped = tail > 0 && next > 0
			carried = tail
			if next <= 0 {
				next = cut
				carried = 0
			}
			para = strings.TrimSpace(para[next:])
		}
		pieces = append(pieces, para)
		continued = append(continued, overlapped)
	}

	// Pack pieces into chunks, carrying the overlap tail across boundaries.
	var chunks []string
	var b strings.Builder
	for i, piece := range pieces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b.Len() > 0 && b.Len()+2+len(piece) > size {
			chunk := b.String()
			chunks = append(chunks, chunk)
			b.Reset()
			// The tail rides only when the piece leaves room for it, which is
			// what keeps size an actual ceiling.
			if tail := overlapTail(chunk, overlap); tail != "" && !continued[i] && len(tail)+2+len(piece) <= size {
				b.WriteString(tail)
			}
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(piece)
	}
	if b.Len() > 0 {
		chunks = append(chunks, b.String())
	}
	return chunks, nil
}

// splitPoint finds where to cut an oversized paragraph: the last space or
// newline inside the size budget, or the size itself backed off any partial
// rune when the paragraph has no break to offer. It never returns zero: when
// the budget is smaller than the first character, that character is the cut.
// Without that, a two-byte é under a one-byte budget backed off to zero and the
// chunk loop spun forever. carried is how many leading bytes repeat the piece
// before this one; a break at or inside them, or separated from them only by
// whitespace, would cut a piece holding no new text, so it does not count as a
// break.
func splitPoint(s string, size, carried int) int {
	cut := strings.LastIndexAny(s[:size], " \n")
	// A break in the first tenth means the budget is nearly all one word;
	// cutting there would emit confetti.
	if cut <= size/10 || cut <= carried || strings.TrimSpace(s[carried:cut]) == "" {
		cut = size
		for cut > 0 && !utf8RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			_, width := utf8.DecodeRuneInString(s)
			cut = width
		}
	}
	return cut
}

// overlapTail returns the last overlap bytes of a chunk, grown to the nearest
// word boundary so the repeated text starts mid-sentence rather than mid-word.
func overlapTail(chunk string, overlap int) string {
	if overlap <= 0 || len(chunk) <= overlap {
		return ""
	}
	tail := chunk[len(chunk)-overlap:]
	if idx := strings.IndexAny(tail, " \n"); idx >= 0 {
		tail = tail[idx+1:]
	} else {
		for len(tail) > 0 && !utf8RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	return strings.TrimSpace(tail)
}

// overlapTailLen is the byte length overlapTail would keep, used when the
// overlap is re-read from the source string rather than copied.
func overlapTailLen(chunk string, overlap int) int {
	return len(overlapTail(chunk, overlap))
}

// utf8RuneStart reports whether a byte begins a UTF-8 rune.
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
