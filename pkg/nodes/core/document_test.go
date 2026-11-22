package core

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalPDF builds a syntactically complete one-page PDF holding one text
// string — synthetic, but byte-for-byte the format a real writer emits for
// the uncompressed case: five objects, a correct xref table, and a Helvetica
// Tj operator. The offsets are computed while writing, since a PDF whose
// xref lies is unreadable by design.
func minimalPDF(text string) []byte {
	var b bytes.Buffer
	offsets := make([]int, 6)
	b.WriteString("%PDF-1.4\n")
	obj := func(num int, body string) {
		offsets[num] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

// zipDoc builds an in-memory zip archive from entry name to content, the
// container both office formats share.
func zipDoc(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, content := range entries {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return b.Bytes()
}

// TestDocumentExtractsRealFormats pins extraction against each container as
// its real writer lays it out: a PDF's text operators, a docx's
// word/document.xml with w:t runs and a w:br, an odt's content.xml with bare
// character data, a tab, and a text:s space run. This is the SOP-to-RAG
// entry point, so paragraph and line structure must survive — the chunker
// downstream packs on exactly those boundaries.
func TestDocumentExtractsRealFormats(t *testing.T) {
	docx := zipDoc(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Step one: acknowledge the ticket.</w:t></w:r><w:r><w:br/><w:t>Then triage.</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Step two: escalate leaks to the on-call plumber.</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	odt := zipDoc(t, map[string]string{
		"content.xml": `<?xml version="1.0"?>` +
			`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:text>` +
			`<text:h>Support SOP</text:h>` +
			`<text:p>Reset the router<text:tab/>weekly.<text:line-break/>Log<text:s/>it.</text:p>` +
			`</office:text></office:body></office:document-content>`,
	})

	cases := []struct {
		name   string
		cfg    string
		data   []byte
		expect string
	}{
		{name: "docx paragraphs, runs, and breaks", cfg: `{"format":"docx"}`, data: docx,
			expect: "Step one: acknowledge the ticket.\nThen triage.\n\nStep two: escalate leaks to the on-call plumber."},
		{name: "odt headings, tabs, breaks, and space runs", cfg: `{"format":"odt"}`, data: odt,
			expect: "Support SOP\n\nReset the router\tweekly.\nLog it."},
	}
	for _, tc := range cases {
		out := runParser(t, DocumentNode{}, tc.cfg, &engine.Msg{Payload: tc.data})
		assert.Equalf(t, tc.expect, stringPayload(t, out), "extraction for %s", tc.name)
	}

	// PDF asserted separately: glyph positioning means the library owns the
	// exact spacing, so the claim is the words in order plus the page count.
	out := runParser(t, DocumentNode{}, `{"format":"pdf"}`,
		&engine.Msg{Payload: minimalPDF("Escalate leaks to the on-call plumber.")})
	assert.Contains(t, stringPayload(t, out), "Escalate leaks to the on-call plumber.")
	assert.Equal(t, 1, out.Meta["pages"], "the page count rides the metadata")
}

// TestDocumentSkipsDocxTabStopDefinitions locks that a paragraph's tab stop
// settings are not read as typed tabs. Word writes each stop as a w:tab inside
// w:pPr, the same element name a tab character in a run has, so a paragraph
// with custom stops came out with one stray tab per stop in front of its text.
func TestDocumentSkipsDocxTabStopDefinitions(t *testing.T) {
	docx := zipDoc(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:pPr><w:tabs><w:tab w:val="left" w:pos="2880"/><w:tab w:val="right" w:pos="9360"/></w:tabs></w:pPr>` +
			`<w:r><w:t>Name</w:t></w:r><w:r><w:tab/><w:t>Value</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	out := runParser(t, DocumentNode{}, `{"format":"docx"}`, &engine.Msg{Payload: docx})
	assert.Equal(t, "Name\tValue", stringPayload(t, out),
		"only the tab typed in a run is text; the two stop definitions are layout")
}

// TestDocumentDeclaredTypeRule pins the no-sniffing contract: with Format
// left on From file type, the declared metadata decides — a filename
// extension resolves the format, and undeclared bytes fail naming the fix
// rather than being guessed at.
func TestDocumentDeclaredTypeRule(t *testing.T) {
	docx := zipDoc(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Declared by path.</w:t></w:r></w:p></w:body></w:document>`,
	})

	out := runParser(t, DocumentNode{}, `{}`,
		&engine.Msg{Payload: docx, Meta: map[string]any{"path": "files/sop.DOCX"}})
	assert.Equal(t, "Declared by path.", stringPayload(t, out),
		"a declared file name resolves the format, case-insensitively")

	parserError(t, DocumentNode{}, `{}`, &engine.Msg{Payload: docx})
	parserError(t, DocumentNode{}, `{}`,
		&engine.Msg{Payload: "not bytes", Meta: map[string]any{"path": "sop.pdf"}})
}

// TestDocumentExpandsODTSpaceRuns locks that a text:s element yields as many
// spaces as its text:c count. A writer stores a run of spaces as one literal
// space followed by text:s with the rest, and each element was read as a
// single space, so aligned columns lost their spacing.
func TestDocumentExpandsODTSpaceRuns(t *testing.T) {
	odt := zipDoc(t, map[string]string{
		"content.xml": `<?xml version="1.0"?>` +
			`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:text>` +
			`<text:p>Port <text:s text:c="3"/>22<text:s/>open</text:p>` +
			`</office:text></office:body></office:document-content>`,
	})
	out := runParser(t, DocumentNode{}, `{"format":"odt"}`, &engine.Msg{Payload: odt})
	assert.Equal(t, "Port    22 open", stringPayload(t, out),
		"A counted run must expand to its count, and an uncounted one to a single space.")
}
