package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/ledongthuc/pdf"
)

// DocumentNode extracts the text of a PDF, Word, or OpenDocument file, which
// is what puts an SOP or a manual within a flow's reach: file-read the bytes,
// extract here, chunk into a memory store with Ingest. Extraction is text
// only — no OCR — so a scanned PDF yields nothing and says so.
type DocumentNode struct{}

// Meta describes the document node type.
func (DocumentNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "document",
		Label:       "Document",
		Folder:      flow.FolderParsers,
		Description: "Extracts the text of a PDF, Word, or OpenDocument file",
		Icon:        "las la-file-alt",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortBinary},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "format", Label: "Format", Type: flow.FieldSelect,
				Default: "",
				Options: []flow.Option{
					{Value: "", Label: "From file type"},
					{Value: "pdf", Label: "PDF"},
					{Value: "docx", Label: "Word (docx)"},
					{Value: "odt", Label: "OpenDocument (odt)"},
				},
				Help: "How the bytes are read. From file type resolves it from the declared metadata — meta.mime, or the extension of meta.path or meta.filename — and undeclared bytes fail rather than being guessed at.",
			},
		},
	}
}

// documentConfig is the decoded document node configuration.
type documentConfig struct {
	Format string `json:"format"`
}

// Execute extracts the document's text as the payload.
func (DocumentNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg documentConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	data, ok := msg.Payload.([]byte)
	if !ok {
		return nil, fmt.Errorf("document payload is %T, not bytes; read the file as binary", msg.Payload)
	}

	format := cfg.Format
	if format == "" {
		format, ok = declaredDocumentFormat(msg.Meta)
		if !ok {
			return nil, fmt.Errorf("the message declares no file type; set Format, or supply meta.mime or a file name in meta.path")
		}
	}

	var text string
	var pages int
	var err error
	switch format {
	case "pdf":
		text, pages, err = pdfText(data)
	case "docx":
		text, err = docxText(data)
	case "odt":
		text, err = odtText(data)
	default:
		return nil, fmt.Errorf("unknown document format %q; the formats are pdf, docx, odt", format)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", format, err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("the %s contains no extractable text; a scanned document needs OCR, which this node does not do", format)
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	if pages > 0 {
		meta["pages"] = pages
	}
	return engine.One("out", &engine.Msg{Payload: text, Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}

// documentMIMEs maps the declared MIME types onto formats.
var documentMIMEs = map[string]string{
	"application/pdf": "pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.oasis.opendocument.text":                                 "odt",
}

// declaredDocumentFormat resolves the format from what the producer declared:
// a MIME type first, then the extension of a declared file name. Reading
// metadata is not sniffing — the bytes themselves are never inspected.
func declaredDocumentFormat(meta map[string]any) (string, bool) {
	for _, key := range []string{"mime", "content_type"} {
		if s, ok := meta[key].(string); ok {
			if format, ok := documentMIMEs[strings.ToLower(strings.TrimSpace(strings.SplitN(s, ";", 2)[0]))]; ok {
				return format, true
			}
		}
	}
	for _, key := range []string{"path", "filename", "name"} {
		s, ok := meta[key].(string)
		if !ok {
			continue
		}
		switch strings.ToLower(path.Ext(s)) {
		case ".pdf":
			return "pdf", true
		case ".docx":
			return "docx", true
		case ".odt":
			return "odt", true
		}
	}
	return "", false
}

// pdfText extracts a PDF's text page by page, rows in reading order. Pieces
// on one row are concatenated as the file drew them, with a space inserted
// when the horizontal gap between two pieces is wide enough to be one — PDF
// text operators encode positions, not word breaks. The library recovers its
// own parse panics; the outer recover contains anything it missed, because a
// hostile file must fail the node rather than the process.
func pdfText(data []byte) (text string, pages int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("malformed PDF: %v", r)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", 0, err
	}
	pages = reader.NumPage()
	var b strings.Builder
	for i := 1; i <= pages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		rows, err := page.GetTextByRow()
		if err != nil {
			return "", 0, fmt.Errorf("page %d: %w", i, err)
		}
		pageStart := b.Len()
		for _, row := range rows {
			if b.Len() > pageStart {
				b.WriteString("\n")
			}
			var prev *pdf.Text
			for i := range row.Content {
				piece := &row.Content[i]
				if prev != nil && wordGap(prev, piece) {
					b.WriteString(" ")
				}
				b.WriteString(piece.S)
				prev = piece
			}
		}
		if b.Len() > pageStart && i < pages {
			b.WriteString("\n\n")
		}
	}
	return b.String(), pages, nil
}

// wordGap reports whether the horizontal gap between two text pieces reads as
// a word break: wider than a third of the font size, roughly a space glyph.
func wordGap(prev, next *pdf.Text) bool {
	gap := next.X - (prev.X + prev.W)
	threshold := prev.FontSize / 3
	if threshold <= 0 {
		threshold = 2
	}
	return gap > threshold
}

// docxText extracts a Word document's text. A .docx is a zip holding
// word/document.xml; paragraphs (w:p) become paragraphs, explicit breaks
// (w:br) newlines, tabs (w:tab) tabs, and every w:t carries the words.
func docxText(data []byte) (string, error) {
	doc, err := zipEntry(data, "word/document.xml")
	if err != nil {
		return "", err
	}
	return officeXMLText(doc, officeDialect{
		paragraph: func(name xml.Name) bool { return name.Local == "p" },
		// Paragraph properties hold tab stop definitions, which are w:tab
		// elements like a typed tab but describe layout.
		properties: func(name xml.Name) bool { return name.Local == "pPr" },
		lineBreak:  func(name xml.Name) bool { return name.Local == "br" || name.Local == "cr" },
		tab:        func(name xml.Name) bool { return name.Local == "tab" },
		text:       func(name xml.Name) bool { return name.Local == "t" },
	})
}

// odtSpaceRun matches ODT's text:s element, a run of spaces collapsed by XML.
// Its text:c attribute counts them.
func odtSpaceRun(name xml.Name) bool { return name.Local == "s" }

// odtText extracts an OpenDocument text file. A .odt is a zip holding
// content.xml; text:p and text:h are paragraphs, text:line-break and text:tab
// their literal selves, and character data inside a paragraph is the words.
func odtText(data []byte) (string, error) {
	doc, err := zipEntry(data, "content.xml")
	if err != nil {
		return "", err
	}
	return officeXMLText(doc, officeDialect{
		paragraph: func(name xml.Name) bool { return name.Local == "p" || name.Local == "h" },
		lineBreak: func(name xml.Name) bool { return name.Local == "line-break" },
		tab:       func(name xml.Name) bool { return name.Local == "tab" },
		space:     odtSpaceRun,
		// text is nil: ODT carries words as bare character data inside the
		// paragraph rather than in a text element of its own.
	})
}

// maxSpaceRun caps the spaces one space-run element may expand to, so a
// document cannot ask for gigabytes of padding with a few bytes of markup.
const maxSpaceRun = 1024

// spaceRunCount reads how many spaces a space-run element stands for: its
// count attribute, or one when it carries none or an unusable one.
func spaceRunCount(el xml.StartElement) int {
	for _, attr := range el.Attr {
		if attr.Name.Local != "c" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(attr.Value))
		if err != nil || n < 1 {
			return 1
		}
		return min(n, maxSpaceRun)
	}
	return 1
}

// zipEntry reads one file out of a zip archive held in memory.
func zipEntry(data []byte, name string) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip container: %w", err)
	}
	for _, f := range archive.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return readCapped(rc, name)
	}
	return nil, fmt.Errorf("no %s in the archive", name)
}

// officeDialect names the elements one office XML dialect uses for the
// structures extraction cares about. A nil matcher means the dialect has no
// such element; a nil text matcher means words are bare character data inside
// the paragraph rather than wrapped in a text element. Everything inside a
// properties element is formatting and contributes no text.
type officeDialect struct {
	paragraph  func(xml.Name) bool
	properties func(xml.Name) bool
	lineBreak  func(xml.Name) bool
	tab        func(xml.Name) bool
	space      func(xml.Name) bool
	text       func(xml.Name) bool
}

// officeXMLText walks an office document's XML and assembles its text:
// paragraphs joined by blank lines, breaks and tabs kept, everything else
// dropped. Both zipped-XML office formats reduce to this walk; only the
// element names differ.
func officeXMLText(doc []byte, d officeDialect) (string, error) {
	matches := func(m func(xml.Name) bool, name xml.Name) bool { return m != nil && m(name) }
	decoder := xml.NewDecoder(bytes.NewReader(doc))
	var paragraphs []string
	var para strings.Builder
	paraDepth := 0
	textDepth := 0
	propDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("malformed document XML: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			if matches(d.properties, t.Name) {
				propDepth++
			}
			if propDepth > 0 {
				continue
			}
			switch {
			case d.paragraph(t.Name):
				paraDepth++
			case d.lineBreak(t.Name):
				if paraDepth > 0 {
					para.WriteString("\n")
				}
			case d.tab(t.Name):
				if paraDepth > 0 {
					para.WriteString("\t")
				}
			case matches(d.space, t.Name):
				if paraDepth > 0 {
					para.WriteString(strings.Repeat(" ", spaceRunCount(t)))
				}
			case matches(d.text, t.Name):
				textDepth++
			}
		case xml.EndElement:
			if propDepth > 0 {
				if matches(d.properties, t.Name) {
					propDepth--
				}
				continue
			}
			switch {
			case d.paragraph(t.Name):
				if paraDepth > 0 {
					paraDepth--
					if paraDepth == 0 {
						if s := para.String(); strings.TrimSpace(s) != "" {
							paragraphs = append(paragraphs, s)
						}
						para.Reset()
					}
				}
			case matches(d.text, t.Name):
				if textDepth > 0 {
					textDepth--
				}
			}
		case xml.CharData:
			inWords := propDepth == 0 && (textDepth > 0 || (d.text == nil && paraDepth > 0))
			if inWords {
				para.Write(t)
			}
		}
	}
	return strings.Join(paragraphs, "\n\n"), nil
}
