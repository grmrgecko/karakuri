package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runParser converts one message through a parser node and returns the single
// emitted result.
func runParser(t *testing.T, node engine.NodeType, cfg string, in *engine.Msg) *engine.Msg {
	t.Helper()
	out, err := node.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	require.NoErrorf(t, err, "%s must convert this input", node.Meta().Type)

	msg := only(out, "out")
	require.NotNilf(t, msg, "%s converts a message rather than dropping it", node.Meta().Type)
	return msg
}

// parserError runs a parser node expecting it to fail.
func parserError(t *testing.T, node engine.NodeType, cfg string, in *engine.Msg) {
	t.Helper()
	_, err := node.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": in})
	require.Errorf(t, err,
		"%s must fail the node rather than emit something that is not what its port declared", node.Meta().Type)
}

// stringPayload asserts the payload is text and returns it.
func stringPayload(t *testing.T, msg *engine.Msg) string {
	t.Helper()
	s, ok := msg.Payload.(string)
	require.Truef(t, ok, "payload is %T, want the string the encoder's port declares", msg.Payload)
	return s
}

// objectPayload asserts the payload is an object and returns it.
func objectPayload(t *testing.T, msg *engine.Msg) map[string]any {
	t.Helper()
	obj, ok := msg.Payload.(map[string]any)
	require.Truef(t, ok, "payload is %T, want the object the decoder's port declares", msg.Payload)
	return obj
}

// TestDecoderAndEncoderAreSeparateNodes states why there are two node types
// rather than one with a direction setting: a node that could go either way
// could not say what its ports held, and the port type is the whole point. Each
// node does one thing, so each port declares one type.
func TestDecoderAndEncoderAreSeparateNodes(t *testing.T) {
	parsed := runParser(t, JSONNode{}, `{}`, &engine.Msg{Payload: `{"a":1}`})
	assert.Equal(t, 1.0, objectPayload(t, parsed)["a"], "the decoder turns text into a document, always")

	encoded := runParser(t, JSONEncodeNode{}, `{}`, &engine.Msg{Payload: map[string]any{"a": 1.0}})
	assert.Equal(t, `{"a":1}`, stringPayload(t, encoded), "the encoder turns a document into text, always")
}

// TestConverterRejectsALeftoverDirection covers a graph saved when one node did
// both jobs. A leftover direction that contradicts the node it now sits on is
// reported rather than ignored: converting the other way would produce a
// plausible message of entirely the wrong shape, and the author would look
// downstream for the cause.
func TestConverterRejectsALeftoverDirection(t *testing.T) {
	parserError(t, JSONNode{}, `{"direction":"encode"}`, &engine.Msg{Payload: map[string]any{}})
	parserError(t, JSONEncodeNode{}, `{"direction":"decode"}`, &engine.Msg{Payload: `{}`})

	// An automatic direction never named one to contradict, so it is accepted.
	runParser(t, JSONNode{}, `{"direction":"auto"}`, &engine.Msg{Payload: `{"a":1}`})
	runParser(t, JSONEncodeNode{}, `{"direction":"auto"}`, &engine.Msg{Payload: map[string]any{"a": 1.0}})
}

// TestParserWritesToATargetProperty covers decoding one field of a document in
// place, which is the common case for a webhook body or an MQTT payload nested
// inside an envelope. The source stays put — a converter reads, it does not
// consume — so the raw text is still there for a later node or for debugging.
func TestParserWritesToATargetProperty(t *testing.T) {
	msg := runParser(t, JSONNode{}, `{"property":"payload.body","target":"payload.parsed"}`,
		&engine.Msg{Payload: map[string]any{"body": `{"ok":true}`}})

	obj := objectPayload(t, msg)
	assert.Equal(t, `{"ok":true}`, obj["body"], "the source stays put: a converter reads rather than consumes")

	parsed, ok := obj["parsed"].(map[string]any)
	require.Truef(t, ok, "parsed = %#v, want the decoded object at the target path", obj["parsed"])
	assert.Equal(t, true, parsed["ok"], "the decoded document must hold what the text said")
}

// TestParserLeavesTheInputMessageAlone covers the copy every converter makes.
// The engine recorded the incoming message in run history and a parallel branch
// may still be reading it, so decoding in place would rewrite both.
func TestParserLeavesTheInputMessageAlone(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"body": `{"ok":true}`}}
	runParser(t, JSONNode{}, `{"property":"payload.body"}`, in)

	assert.Equal(t, `{"ok":true}`, in.Payload.(map[string]any)["body"],
		"the input must be untouched: run history and any parallel branch are still holding it")

	// A property that is not there is a configuration mistake, not an empty
	// document: decoding nothing would emit an object the port promised but
	// that says nothing about what arrived.
	parserError(t, JSONNode{}, `{"property":"payload.nope"}`, &engine.Msg{Payload: map[string]any{}})
}

// TestJSONParsesAndIndents covers the decoder over a document with mixed
// shapes, and the indent setting, which exists because JSON built by a flow is
// often read by a person — a log file or a file written to disk.
func TestJSONParsesAndIndents(t *testing.T) {
	msg := runParser(t, JSONNode{}, `{"direction":"decode"}`,
		&engine.Msg{Payload: `{"list":[1,2],"name":"kitchen"}`})
	obj := objectPayload(t, msg)
	assert.Equal(t, "kitchen", obj["name"], "a scalar field must decode")
	assert.Len(t, obj["list"], 2, "an array field must decode as an array: %#v", obj["list"])

	indented := runParser(t, JSONEncodeNode{}, `{"indent":"2"}`,
		&engine.Msg{Payload: map[string]any{"name": "kitchen"}})
	assert.Equal(t, "{\n  \"name\": \"kitchen\"\n}", stringPayload(t, indented),
		"the indent setting must produce the layout a person reads, not compact output")
}

// TestJSONDoesNotEscapeHTML pins a deliberate departure from Go's default.
// encoding/json escapes <, > and & to \u003c and friends, which is safe in a
// script tag and unreadable everywhere else — a flow publishing to MQTT or
// writing a file wants the characters it put in.
func TestJSONDoesNotEscapeHTML(t *testing.T) {
	msg := runParser(t, JSONEncodeNode{}, `{}`, &engine.Msg{Payload: map[string]any{"html": "<b>&</b>"}})
	assert.Equal(t, `{"html":"<b>&</b>"}`, stringPayload(t, msg),
		"HTML characters must stay readable rather than becoming the \\u003c escapes Go emits by default")
}

// TestJSONRejectsBadInput covers the decoder's refusals against the decodes
// above: text that is not JSON, and a payload that is not text at all. Both
// fail the node so the failure branch and the retry policy apply.
func TestJSONRejectsBadInput(t *testing.T) {
	parserError(t, JSONNode{}, `{}`, &engine.Msg{Payload: `{"a":`})
	parserError(t, JSONNode{}, `{}`, &engine.Msg{Payload: 42.0})
}

// TestYAMLRoundTrips covers the YAML pair. The number is the case worth
// pinning: YAML has its own integer type, and it has to arrive as float64 like
// every other decoded document, or an expression comparing a YAML number
// behaves differently from one comparing a JSON number.
func TestYAMLRoundTrips(t *testing.T) {
	msg := runParser(t, YAMLNode{}, `{}`, &engine.Msg{Payload: "name: kitchen\ncount: 3\ntags:\n  - a\n  - b\n"})
	obj := objectPayload(t, msg)
	assert.Equal(t, "kitchen", obj["name"], "a scalar field must decode")
	assert.Equal(t, 3.0, obj["count"],
		"a YAML integer must arrive as float64 like every other decoded number, or expressions compare it differently")
	assert.Len(t, obj["tags"], 2, "a sequence must decode as an array: %#v", obj["tags"])

	back := runParser(t, YAMLEncodeNode{}, `{}`, &engine.Msg{Payload: obj})
	assert.Contains(t, stringPayload(t, back), "name: kitchen", "the decoded document must encode back to YAML")
}

// TestYAMLRejectsBadInput covers the YAML pair's refusals: text whose
// indentation does not parse, and an indent setting that is not a number.
func TestYAMLRejectsBadInput(t *testing.T) {
	parserError(t, YAMLNode{}, `{"direction":"decode"}`, &engine.Msg{Payload: "a:\n- b\n  c: d\n"})
	parserError(t, YAMLEncodeNode{}, `{"indent":"nope"}`, &engine.Msg{Payload: map[string]any{}})
}

// TestYAMLDecodesOnlyJSONShapedValues locks that a YAML document decodes to
// values the rest of the engine can carry. The YAML library reads an unquoted
// date as a time value and ".nan" or ".inf" as a non-finite float, and neither
// is a JSON shape: the date compared as a quoted RFC 3339 string in a switch
// rule, and the float made the message impossible to store or send. Both stay
// the text that was written, while an ordinary number still decodes.
func TestYAMLDecodesOnlyJSONShapedValues(t *testing.T) {
	msg := runParser(t, YAMLNode{}, `{}`,
		&engine.Msg{Payload: "day: 2024-03-09\nat: 2024-03-09T10:30:00Z\nreading: .nan\nlimit: .inf\nlevel: 21.5\n"})
	obj := objectPayload(t, msg)

	assert.Equal(t, "2024-03-09", obj["day"], "an unquoted date must arrive as the text the document holds")
	assert.Equal(t, "2024-03-09T10:30:00Z", obj["at"], "an unquoted timestamp must arrive as the text the document holds")
	assert.Equal(t, ".nan", obj["reading"], "a non-finite float must stay text, since JSON cannot carry it")
	assert.Equal(t, ".inf", obj["limit"], "an infinite float must stay text, since JSON cannot carry it")
	assert.Equal(t, 21.5, obj["level"], "a finite number beside them must still decode as a number")
	_, err := json.Marshal(obj)
	assert.NoError(t, err, "a decoded document must always serialize")

	_, out := runTemplate(t, `{"template":"since: 2024-03-09","output_format":"yaml"}`, &engine.Msg{})
	assert.Equal(t, map[string]any{"since": "2024-03-09"}, out.Payload,
		"a template read as YAML must follow the same rule as the YAML node")
}

// TestXMLParsesAttributesTextAndChildren pins how XML's three kinds of content
// map onto a JSON-shaped document, which is a mapping this codebase chose
// rather than one the format defines: attributes go under "$", and repeated
// elements become an array. An author writes expressions against those names,
// so they are the contract.
func TestXMLParsesAttributesTextAndChildren(t *testing.T) {
	msg := runParser(t, XMLNode{}, `{"direction":"decode"}`, &engine.Msg{
		Payload: `<?xml version="1.0"?><note id="7"><to>Tove</to><to>Jani</to></note>`})

	note, ok := objectPayload(t, msg)["note"].(map[string]any)
	require.True(t, ok, "the root element becomes the document's single key")

	attrs, ok := note["$"].(map[string]any)
	require.Truef(t, ok, "attributes live under $, got %#v", note["$"])
	assert.Equal(t, "7", attrs["id"], "an attribute must be readable by name under $")

	assert.Equal(t, []any{"Tove", "Jani"}, note["to"],
		"an element appearing twice becomes an array, in document order")
}

// TestXMLAutoArrayCollapsesSingleChildren covers auto mode, which collapses an
// element appearing once to the value itself. Most elements in a real document
// are unique, so without this every expression would have to index [0].
func TestXMLAutoArrayCollapsesSingleChildren(t *testing.T) {
	msg := runParser(t, XMLNode{}, `{"direction":"decode","array_mode":"auto"}`,
		&engine.Msg{Payload: `<note><to>Tove</to><from>Jani</from></note>`})

	note, ok := objectPayload(t, msg)["note"].(map[string]any)
	require.True(t, ok, "the root element becomes the document's single key")
	assert.Equal(t, "Tove", note["to"], "an element appearing once collapses to its value, not a one-element array")
	assert.Equal(t, "Jani", note["from"], "an element appearing once collapses to its value, not a one-element array")
}

// TestXMLBuildsADocument covers the encoder using the same mapping in reverse:
// "$" becomes attributes and an array becomes repeated elements.
func TestXMLBuildsADocument(t *testing.T) {
	msg := runParser(t, XMLEncodeNode{}, `{}`, &engine.Msg{
		Payload: map[string]any{
			"note": map[string]any{
				"$":  map[string]any{"id": "7"},
				"to": []any{"Tove", "Jani"},
			},
		}})

	assert.Equal(t, `<note id="7"><to>Tove</to><to>Jani</to></note>`, stringPayload(t, msg),
		"$ must become attributes and an array must become repeated elements, the decoder's mapping in reverse")
}

// TestXMLRoundTrips is the check the two halves above cannot make separately: a
// real document decoded and re-encoded must come back byte for byte. Any
// disagreement between the two mappings shows up here rather than in a device
// silently rejecting what a flow sent it.
func TestXMLRoundTrips(t *testing.T) {
	doc := `<reading unit="C"><sensor>kitchen</sensor><value>21.5</value></reading>`
	parsed := runParser(t, XMLNode{}, `{"direction":"decode","array_mode":"auto"}`,
		&engine.Msg{Payload: doc})
	back := runParser(t, XMLEncodeNode{}, `{}`, parsed)

	assert.Equal(t, doc, stringPayload(t, back),
		"the decoder and encoder must agree exactly, or a document a flow rebuilt is not the one it read")
}

// TestXMLRejectsBadInput covers the XML pair's refusals. The encoder ones are
// about naming: XML needs exactly one root element and legal element names, and
// neither can be invented on the author's behalf without producing a document
// the receiver rejects.
func TestXMLRejectsBadInput(t *testing.T) {
	parserError(t, XMLNode{}, `{"direction":"decode"}`, &engine.Msg{Payload: `<note><to>Tove</note>`})
	parserError(t, XMLNode{}, `{"direction":"decode"}`, &engine.Msg{Payload: `   `})
	// Two top-level keys leave no way to name the root.
	parserError(t, XMLEncodeNode{}, `{}`,
		&engine.Msg{Payload: map[string]any{"a": "1", "b": "2"}})
	// A key that is not a legal element name would produce unreadable output.
	parserError(t, XMLEncodeNode{}, `{"root":"reading"}`,
		&engine.Msg{Payload: map[string]any{"not a name": "1"}})
}

// TestXMLReadsADeclaredEncoding locks that a document declaring an encoding
// other than UTF-8 is decoded through it. Device APIs and older feeds still
// answer in ISO-8859-1 or Windows-1252, and the parser refused every one of
// them outright because it had no way to read the declared charset. A
// document with no declaration is still read as the UTF-8 it is.
func TestXMLReadsADeclaredEncoding(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"ISO-8859-1", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><room>caf\xe9</room>"},
		{"windows-1252", "<?xml version=\"1.0\" encoding=\"windows-1252\"?><room>caf\xe9</room>"},
		{"UTF-8 declared", "<?xml version=\"1.0\" encoding=\"UTF-8\"?><room>caf\u00e9</room>"},
		{"no declaration", "<room>caf\u00e9</room>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := runParser(t, XMLNode{}, `{}`, &engine.Msg{Payload: c.doc})
			assert.Equal(t, "caf\u00e9", objectPayload(t, msg)["room"],
				"the text must arrive as the characters the document encodes")
		})
	}

	parserError(t, XMLNode{}, `{}`,
		&engine.Msg{Payload: "<?xml version=\"1.0\" encoding=\"no-such-charset\"?><room>x</room>"})
}

// TestXMLWrapsInAConfiguredRoot covers the root setting, which is what lets a
// flat payload be encoded at all — it supplies the single root element XML
// requires, and it is why the encoder's input port stops demanding an object
// once a root is configured.
func TestXMLWrapsInAConfiguredRoot(t *testing.T) {
	msg := runParser(t, XMLEncodeNode{}, `{"root":"reading","indent":"2"}`,
		&engine.Msg{Payload: map[string]any{"sensor": "kitchen", "value": 21.5}})

	assert.Equal(t, "<reading>\n  <sensor>kitchen</sensor>\n  <value>21.5</value>\n</reading>",
		stringPayload(t, msg),
		"the configured root must wrap the payload's fields, indented as asked")
}

// TestCSVParsesAHeaderIntoObjects covers the ordinary CSV: a header row names
// the fields, and numeric-looking values are read as numbers so a flow can do
// arithmetic on them without a conversion node.
func TestCSVParsesAHeaderIntoObjects(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode"}`,
		&engine.Msg{Payload: "sensor,value\nkitchen,21.5\nporch,4\n"})

	rows, ok := msg.Payload.([]any)
	require.Truef(t, ok, "payload is %T, want an array of rows", msg.Payload)
	require.Len(t, rows, 2, "the header is not a row: %#v", rows)

	first, ok := rows[0].(map[string]any)
	require.True(t, ok, "a row with a header becomes an object keyed by column name")
	assert.Equal(t, "kitchen", first["sensor"], "the header row names the fields")
	assert.Equal(t, 21.5, first["value"], "numeric text is read as a number, so a flow can do arithmetic on it")
}

// TestCSVKeepsFieldsAsTextWhenAsked pins why parsing numbers has to be
// optional. A postcode, an account number, or a device id can be all digits
// with a leading zero, and reading it as a number silently destroys the value —
// 01234 becomes 1234 and no longer matches anything.
func TestCSVKeepsFieldsAsTextWhenAsked(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode","parse_numbers":false}`,
		&engine.Msg{Payload: "zip\n01234\n"})

	rows, ok := msg.Payload.([]any)
	require.Truef(t, ok, "payload is %T, want an array of rows", msg.Payload)
	assert.Equal(t, "01234", rows[0].(map[string]any)["zip"],
		"the leading zero must survive: read as a number this becomes 1234 and matches nothing")
}

// TestCSVNamedColumnsWithoutAHeader covers a file with no header row, which is
// what most machine-generated CSV looks like. Naming the columns in config is
// what turns those rows into objects an expression can read by name.
func TestCSVNamedColumnsWithoutAHeader(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode","columns":"sensor,value","header":false}`,
		&engine.Msg{Payload: "kitchen,21.5\n"})

	rows, ok := msg.Payload.([]any)
	require.Truef(t, ok, "payload is %T, want an array of rows", msg.Payload)
	assert.Equal(t, "kitchen", rows[0].(map[string]any)["sensor"],
		"configured column names must key the rows, since there is no header to take them from")
}

// TestCSVWithoutColumnNamesGivesArrays covers a file with neither a header nor
// configured names. A row becomes an array rather than an object with invented
// keys, so nothing has to be made up and an expression indexes by position.
func TestCSVWithoutColumnNamesGivesArrays(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode","header":false}`,
		&engine.Msg{Payload: "kitchen,21.5\n"})

	row, ok := msg.Payload.([]any)[0].([]any)
	require.True(t, ok, "with nothing to name the columns, a row is an array")
	assert.Equal(t, []any{"kitchen", 21.5}, row,
		"the fields keep their order and their parsed types, with no invented keys")
}

// TestCSVFansOutARowPerMessage covers the messages output, which is how a flow
// processes a file row by row rather than as one array. Each part carries a
// group frame so a join downstream can rebuild the set, and the frame's count
// is what tells the join when it is complete.
func TestCSVFansOutARowPerMessage(t *testing.T) {
	out, err := CSVNode{}.Execute(context.Background(), newTestCtx(`{"direction":"decode","output":"messages"}`, nil),
		map[string]*engine.Msg{"in": {Payload: "sensor,value\nkitchen,21.5\nporch,4\n"}})
	require.NoError(t, err, "a CSV fanned out per row must convert")

	parts := out["out"]
	require.Len(t, parts, 2, "one message per data row, with the header excluded")

	for i, p := range parts {
		require.Lenf(t, p.Group, 1, "part %d must carry exactly the frame this fan-out pushed: %+v", i, p.Group)
		assert.Equalf(t, i, p.Group[0].Index, "part %d must know its position in the set", i)
		assert.Equalf(t, 2, p.Group[0].Count, "part %d must carry the set size, which is how a join knows it is complete", i)
	}
	assert.Equal(t, "porch", parts[1].Payload.(map[string]any)["sensor"],
		"the parts must be emitted in file order")
}

// TestCSVProducesTextFromObjects covers the encoder with configured columns,
// which is what fixes both the order and which fields are written.
func TestCSVProducesTextFromObjects(t *testing.T) {
	msg := runParser(t, CSVEncodeNode{}, `{"columns":"sensor,value"}`,
		&engine.Msg{Payload: []any{
			map[string]any{"sensor": "kitchen", "value": 21.5},
			map[string]any{"sensor": "porch", "value": 4.0},
		}})

	assert.Equal(t, "sensor,value\nkitchen,21.5\nporch,4\n", stringPayload(t, msg),
		"the configured columns fix both the header and the field order in every row")
}

// TestCSVTakesColumnsFromTheRows covers the encoder with no configured columns.
// The union of the rows' keys is used rather than the first row's, so a field
// only some rows carry still reaches the file; the rows that lack it get an
// empty cell, which is what keeps every row the same width.
func TestCSVTakesColumnsFromTheRows(t *testing.T) {
	msg := runParser(t, CSVEncodeNode{}, `{}`,
		&engine.Msg{Payload: []any{
			map[string]any{"sensor": "kitchen"},
			map[string]any{"sensor": "porch", "unit": "C"},
		}})

	assert.Equal(t, "sensor,unit\nkitchen,\nporch,C\n", stringPayload(t, msg),
		"unit appears only on the second row and must still become a column, with an empty cell on the first")
}

// TestCSVHonorsATabSeparator covers tab-separated data, which is common enough
// in exported files that a CSV node that could not read it would send authors
// to a script node instead.
func TestCSVHonorsATabSeparator(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode","separator":"\\t"}`,
		&engine.Msg{Payload: "sensor\tvalue\nkitchen\t21.5\n"})

	assert.Equal(t, 21.5, msg.Payload.([]any)[0].(map[string]any)["value"],
		"the tab must be used as the separator, or the whole line reads as one field")
}

// TestCSVKeepsEmptyCellsWithAWhitespaceSeparator covers tab- and
// space-separated data holding an empty cell. encoding/csv applies
// TrimLeadingSpace even when the separator is itself white space, so trimming
// swallowed the separator that ends an empty cell and every later value moved
// one column left. The comma row keeps the padding trim that other separators
// rely on.
func TestCSVKeepsEmptyCellsWithAWhitespaceSeparator(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		text string
	}{
		{"tab", `{"separator":"\\t"}`, "sensor\tunit\tvalue\nkitchen\t\t21.5\n\tC\t19\n"},
		{"space", `{"separator":" "}`, "sensor unit value\nkitchen  21.5\n C 19\n"},
		{"comma with padding", `{}`, "sensor, unit, value\nkitchen, , 21.5\n, C, 19\n"},
	}
	for _, tc := range tests {
		msg := runParser(t, CSVNode{}, tc.cfg, &engine.Msg{Payload: tc.text})
		rows, ok := msg.Payload.([]any)
		require.Truef(t, ok, "%s: payload = %#v, want rows", tc.name, msg.Payload)
		require.Lenf(t, rows, 2, "%s: two data rows follow the header", tc.name)
		assert.Equalf(t, map[string]any{"sensor": "kitchen", "unit": "", "value": 21.5}, rows[0],
			"%s: an empty middle cell must stay in its column, or the value lands under unit", tc.name)
		assert.Equalf(t, map[string]any{"sensor": "", "unit": "C", "value": 19.0}, rows[1],
			"%s: an empty first cell must stay in its column, or every value shifts left", tc.name)
	}
}

// TestCSVRejectsBadInput covers the CSV pair's refusals. The mismatched-width
// row is the one that matters operationally: a file whose rows do not line up
// with the named columns would otherwise produce objects with fields silently
// shifted into the wrong keys.
func TestCSVRejectsBadInput(t *testing.T) {
	parserError(t, CSVNode{}, `{"direction":"decode","separator":"||"}`, &engine.Msg{Payload: "a,b\n"})
	parserError(t, CSVNode{}, `{"direction":"decode","output":"sideways"}`, &engine.Msg{Payload: "a\n1\n"})
	// More fields than named columns means the row does not line up.
	parserError(t, CSVNode{}, `{"direction":"decode","columns":"a","header":false}`,
		&engine.Msg{Payload: "1,2\n"})
	parserError(t, CSVEncodeNode{}, `{}`, &engine.Msg{Payload: 42.0})
}

const testHTML = `<html><body>
  <ul id="rooms">
    <li class="room"><a href="/kitchen">Kitchen</a></li>
    <li class="room"><a href="/porch">Porch</a></li>
  </ul>
</body></html>`

// TestHTMLTakesTextBySelector covers scraping text out of a page, which is the
// fallback for a device or a service with no API. Matches come back in document
// order, since that is the order a person reading the page would see them in.
func TestHTMLTakesTextBySelector(t *testing.T) {
	msg := runParser(t, HTMLNode{}, `{"selector":"li.room a"}`, &engine.Msg{Payload: testHTML})

	assert.Equal(t, []any{"Kitchen", "Porch"}, msg.Payload,
		"every match must be returned, in document order")
}

// TestHTMLTakesAnAttribute covers reading an attribute rather than text, which
// is how a link or a data- value is scraped off the same elements.
func TestHTMLTakesAnAttribute(t *testing.T) {
	msg := runParser(t, HTMLNode{}, `{"selector":"li.room a","output":"attribute","attribute":"href"}`,
		&engine.Msg{Payload: testHTML})

	assert.Equal(t, []any{"/kitchen", "/porch"}, msg.Payload,
		"the named attribute must be taken from every match, not the element's text")
}

// TestHTMLTakesEveryAttributeWhenUnnamed covers asking for attributes without
// naming one: the whole set comes back as an object, so a flow can look at what
// an element carries without knowing in advance.
func TestHTMLTakesEveryAttributeWhenUnnamed(t *testing.T) {
	msg := runParser(t, HTMLNode{}, `{"selector":"ul","output":"attribute"}`,
		&engine.Msg{Payload: testHTML})

	attrs, ok := msg.Payload.([]any)[0].(map[string]any)
	require.True(t, ok, "with no attribute named, each match is an object of all its attributes")
	assert.Equal(t, "rooms", attrs["id"], "every attribute must be readable by name")
}

// TestHTMLTakesInnerHTML covers the third output, for when the markup inside a
// match matters rather than its text — a fragment to be parsed further, or a
// value carried in a nested tag.
func TestHTMLTakesInnerHTML(t *testing.T) {
	msg := runParser(t, HTMLNode{}, `{"selector":"li.room","output":"html"}`,
		&engine.Msg{Payload: testHTML})

	first, ok := msg.Payload.([]any)[0].(string)
	require.True(t, ok, "inner HTML comes back as text")
	assert.Contains(t, first, `href="/kitchen"`, "the markup inside the match must be returned, not just its text")
}

// TestHTMLFansOutAMatchPerMessage covers the messages output, so a flow can act
// on each match separately. Each part carries a group frame, exactly as the CSV
// fan-out does, so the same join rebuilds either.
func TestHTMLFansOutAMatchPerMessage(t *testing.T) {
	out, err := HTMLNode{}.Execute(context.Background(),
		newTestCtx(`{"selector":"li.room a","result":"messages"}`, nil),
		map[string]*engine.Msg{"in": {Payload: testHTML}})
	require.NoError(t, err, "a selector fanned out per match must run")

	parts := out["out"]
	require.Len(t, parts, 2, "one message per match")
	assert.Equal(t, "Porch", parts[1].Payload, "the parts must be emitted in document order")
	require.NotEmpty(t, parts[1].Group, "each part must carry a group frame so a join can rebuild the set")
	assert.Equal(t, 2, parts[1].Group[0].Count, "the frame's count is how a join knows the set is complete")
}

// TestHTMLMatchingNothing covers a selector that matches nothing. It is not a
// failure — a page legitimately may not contain the element today — so the
// array output is empty and the messages output emits nothing at all, rather
// than one message with a null payload that a downstream node would act on.
func TestHTMLMatchingNothing(t *testing.T) {
	msg := runParser(t, HTMLNode{}, `{"selector":"table"}`, &engine.Msg{Payload: testHTML})
	assert.Empty(t, msg.Payload, "no matches is an empty array, not a failure: the element may simply not be there today")

	out, err := HTMLNode{}.Execute(context.Background(),
		newTestCtx(`{"selector":"table","result":"messages"}`, nil),
		map[string]*engine.Msg{"in": {Payload: testHTML}})
	require.NoError(t, err, "a selector matching nothing must not fail the node")
	assert.Empty(t, out["out"],
		"no matches means no messages, not one carrying null for a downstream node to act on")
}

// TestHTMLRejectsBadInput pairs with the empty match above, and the distinction
// is the whole point: a selector that matches nothing is fine, while a selector
// that will not compile is a typo. If the two produced the same result an
// author would spend a long time looking at the page rather than the selector.
func TestHTMLRejectsBadInput(t *testing.T) {
	parserError(t, HTMLNode{}, `{"selector":"li[["}`, &engine.Msg{Payload: testHTML})
	parserError(t, HTMLNode{}, `{}`, &engine.Msg{Payload: testHTML})
	parserError(t, HTMLNode{}, `{"selector":"li","output":"sideways"}`, &engine.Msg{Payload: testHTML})
	parserError(t, HTMLNode{}, `{"selector":"li"}`, &engine.Msg{Payload: 42.0})
}

// TestCSVKeepsNonFiniteWordsAsText locks that number parsing only produces
// numbers JSON can carry. strconv.ParseFloat accepts "NaN", "Inf", and
// "Infinity" in any case, and a row holding one of those floats cannot be
// stored in run history or sent anywhere, since encoding/json refuses it.
func TestCSVKeepsNonFiniteWordsAsText(t *testing.T) {
	rows, err := decodeCSV("name,value\nInf,NaN\ninfinity,1.5\n", csvConfig{}, ',')
	require.NoError(t, err, "the text is valid CSV")
	require.Len(t, rows, 2, "one object per data row")

	assert.Equal(t, map[string]any{"name": "Inf", "value": "NaN"}, rows[0],
		"non-finite words are text, not numbers")
	assert.Equal(t, map[string]any{"name": "infinity", "value": 1.5}, rows[1],
		"a real number on the same sheet still parses")
	_, err = json.Marshal(rows)
	assert.NoError(t, err, "decoded rows must always serialize")
}

// TestSpreadsheetKeepsNonFiniteWordsAsText locks that a sheet cell reading
// "NaN" or "Inf" stays text. strconv.ParseFloat accepts those words in any
// case, and a row holding the resulting float cannot be stored in run history
// or sent anywhere, since encoding/json refuses it.
func TestSpreadsheetKeepsNonFiniteWordsAsText(t *testing.T) {
	rows := []any{map[string]any{"name": "Inf", "state": "NaN", "value": 1.5}}
	written := runNode(t, SpreadsheetEncodeNode{}, `{"columns":"name,state,value"}`, &engine.Msg{Payload: rows})

	out := runNode(t, SpreadsheetNode{}, `{}`, &engine.Msg{Payload: written["out"][0].Payload})
	read, ok := out["out"][0].Payload.([]any)
	require.True(t, ok, "the sheet reads back as a list of rows")
	require.Len(t, read, 1, "one object per data row")

	assert.Equal(t, map[string]any{"name": "Inf", "state": "NaN", "value": 1.5}, read[0],
		"non-finite words are text, and a real number beside them still parses")
	_, err := json.Marshal(read)
	assert.NoError(t, err, "decoded rows must always serialize")
}

// TestCSVIgnoresALeadingByteOrderMark locks that a UTF-8 byte order mark is
// not part of the first column's name. Spreadsheet exports start with one, and
// keeping it left the first column keyed by an invisible character, so a path
// such as payload.sensor found nothing in a file that plainly had the column.
func TestCSVIgnoresALeadingByteOrderMark(t *testing.T) {
	msg := runParser(t, CSVNode{}, `{"direction":"decode"}`,
		&engine.Msg{Payload: "\xEF\xBB\xBFsensor,value\nkitchen,21.5\n"})

	rows, ok := msg.Payload.([]any)
	require.Truef(t, ok, "payload is %T, want the rows as a list", msg.Payload)
	require.Len(t, rows, 1, "the one data row must be parsed")
	row, ok := rows[0].(map[string]any)
	require.Truef(t, ok, "row is %T, want an object keyed by column name", rows[0])
	assert.Equal(t, "kitchen", row["sensor"],
		"the first column must be reachable by the name the header shows, without the byte order mark")
}
