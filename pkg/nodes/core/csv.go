package core

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// CSVNode reads CSV text into message data, turning each row into an object
// keyed by the column names, either as one array or as a message per row.
type CSVNode struct{}

func (CSVNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "csv",
		Label:       "CSV",
		Folder:      flow.FolderParsers,
		Description: "Parses CSV text into message data",
		Icon:        "las la-table",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortObject),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:          "columns",
				Label:       "Columns",
				Type:        flow.FieldString,
				Placeholder: "name,temperature,unit",
				Help: "Comma-separated column names, which name the parsed fields. Empty takes " +
					"them from the header row.",
			},
			csvSeparatorField(),
			{
				ID:      "header",
				Label:   "Header row",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "The first row holds the column names.",
			},
			{
				ID:      "output",
				Label:   "Parse into",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "One message holding every row"},
					{Value: "messages", Label: "A message per row"},
				},
				Help: "A message per row fans out like a split, so a join downstream can put the " +
					"rows back together. Either way a row is an object.",
			},
			{
				ID:      "parse_numbers",
				Label:   "Read numeric fields as numbers",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Turn off to keep every field as text, which preserves leading zeros.",
			},
		},
	}
}

// CSVEncodeNode writes message data out as CSV text.
type CSVEncodeNode struct{}

func (CSVEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "csv-encode",
		Label:       "CSV Encode",
		Folder:      flow.FolderEncoders,
		Description: "Writes message data out as CSV text",
		Icon:        "las la-table",
		// CSV is rows of fields, so there has to be an array or object to
		// write, not a bare scalar.
		Inputs:  converterInput(flow.PortObject),
		Outputs: converterOutput(flow.PortString),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:          "columns",
				Label:       "Columns",
				Type:        flow.FieldString,
				Placeholder: "name,temperature,unit",
				Help: "Comma-separated column names setting the column order. Empty takes them " +
					"from the first row.",
			},
			csvSeparatorField(),
			{
				ID:      "header",
				Label:   "Header row",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Write the column names as the first row.",
			},
		},
	}
}

// csvSeparatorField is the shared field separator config field.
func csvSeparatorField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "separator",
		Label:       "Separator",
		Type:        flow.FieldString,
		Default:     ",",
		Placeholder: ",",
		Help:        "One character. \\t is understood.",
	}
}

// csvConfig is the decoded configuration both CSV nodes share.
type csvConfig struct {
	converterConfig
	Columns      string `json:"columns"`
	Separator    string `json:"separator"`
	Header       *bool  `json:"header"`
	Output       string `json:"output"`
	ParseNumbers *bool  `json:"parse_numbers"`
}

// headerRow reports whether the first row is a header, defaulting to yes.
func (c csvConfig) headerRow() bool {
	return c.Header == nil || *c.Header
}

// numbers reports whether numeric-looking fields become numbers, defaulting to
// yes.
func (c csvConfig) numbers() bool {
	return c.ParseNumbers == nil || *c.ParseNumbers
}

// columnNames returns the configured column names, trimmed and without blanks.
func (c csvConfig) columnNames() []string {
	raw := strings.TrimSpace(c.Columns)
	if raw == "" {
		return nil
	}
	var out []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// Execute parses CSV from the configured property.
func (CSVNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg csvConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if err := requireDirection(cfg.Direction, parserDecode); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	comma, err := csvSeparator(cfg.Separator)
	if err != nil {
		return nil, err
	}
	text, err := parserText(value, property)
	if err != nil {
		return nil, err
	}
	rows, err := decodeCSV(text, cfg, comma)
	if err != nil {
		return nil, fmt.Errorf("invalid CSV in %q: %w", property, err)
	}

	switch cfg.Output {
	case "", "array":
		out, err := parserResult(msg, property, cfg.Target, rows)
		if err != nil {
			return nil, err
		}
		return engine.One("out", out), nil

	case "messages":
		if len(rows) == 0 {
			// An empty document means no downstream work, which is a valid
			// outcome for a file with only a header.
			ec.Debug(nil, "csv produced no rows")
			return nil, nil
		}
		return parserFanOut(msg, property, cfg.Target, rows)

	default:
		return nil, fmt.Errorf("unknown output mode %q", cfg.Output)
	}
}

// Execute produces CSV text from the configured property.
func (CSVEncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg csvConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if err := requireDirection(cfg.Direction, parserEncode); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	comma, err := csvSeparator(cfg.Separator)
	if err != nil {
		return nil, err
	}
	text, err := encodeCSV(value, cfg, comma)
	if err != nil {
		return nil, fmt.Errorf("cannot produce CSV from %q: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, text)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// csvSeparator resolves the field separator, which must be a single character.
func csvSeparator(raw string) (rune, error) {
	sep := unescapeSeparator(raw)
	if sep == "" {
		return ',', nil
	}
	runes := []rune(sep)
	if len(runes) != 1 {
		return 0, fmt.Errorf("separator %q must be a single character", raw)
	}
	return runes[0], nil
}

// decodeCSV parses text into one value per row: an object when column names are
// known, an array of fields when they are not.
func decodeCSV(text string, cfg csvConfig, comma rune) ([]any, error) {
	// A UTF-8 byte order mark, which spreadsheet exports begin with, would
	// otherwise become part of the first column's name.
	text = strings.TrimPrefix(text, "\xEF\xBB\xBF")
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = comma
	// Rows in the wild are ragged and quote loosely; failing the whole document
	// over one of them leaves the flow with nothing.
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	// A white space separator is not padding: trimming it would swallow the
	// separator that ends an empty cell and shift the rest of the row left.
	reader.TrimLeadingSpace = !unicode.IsSpace(comma)

	columns := cfg.columnNames()
	rows := make([]any, 0, 16)
	first := true
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		// The header names the columns unless they were configured, in which
		// case it is only skipped.
		if first {
			first = false
			if cfg.headerRow() {
				if len(columns) == 0 {
					columns = append(columns, record...)
				}
				continue
			}
		}
		// A trailing newline produces one empty record, which is not a row.
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			continue
		}

		if len(columns) == 0 {
			values := make([]any, 0, len(record))
			for _, field := range record {
				values = append(values, csvField(field, cfg.numbers()))
			}
			rows = append(rows, values)
			continue
		}
		if len(record) > len(columns) {
			return nil, fmt.Errorf("row %d has %d fields but only %d columns are named",
				len(rows)+1, len(record), len(columns))
		}
		row := make(map[string]any, len(record))
		for i, field := range record {
			row[columns[i]] = csvField(field, cfg.numbers())
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// csvField reads one field, optionally as a number.
func csvField(field string, numbers bool) any {
	if !numbers {
		return field
	}
	trimmed := strings.TrimSpace(field)
	if trimmed == "" {
		return field
	}
	// ParseFloat also reads "NaN" and "Inf", which are words in a sheet and
	// values JSON cannot carry.
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return n
	}
	return field
}

// encodeCSV writes rows back out as text. Rows may be objects keyed by column
// name or arrays of fields; a single object is written as one row.
func encodeCSV(value any, cfg csvConfig, comma rune) (string, error) {
	rows, err := csvRows(value)
	if err != nil {
		return "", err
	}

	// Column names come from the configuration, or from every key the rows use
	// so no field is dropped.
	columns := cfg.columnNames()
	if len(columns) == 0 {
		columns = csvColumnsFrom(rows)
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Comma = comma
	if cfg.headerRow() && len(columns) > 0 {
		if err := writer.Write(columns); err != nil {
			return "", err
		}
	}
	for i, row := range rows {
		record, err := csvRecord(row, columns)
		if err != nil {
			return "", fmt.Errorf("row %d: %w", i+1, err)
		}
		if err := writer.Write(record); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// csvRows normalizes the input into a list of rows.
func csvRows(value any) ([]any, error) {
	switch t := value.(type) {
	case []any:
		return t, nil
	case map[string]any:
		return []any{t}, nil
	default:
		return nil, fmt.Errorf("need an array of rows or a single object, got %T", value)
	}
}

// csvColumnsFrom collects the column names the rows use, in a stable order.
func csvColumnsFrom(rows []any) []string {
	seen := map[string]bool{}
	var columns []string
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range sortedKeys(obj) {
			if !seen[k] {
				seen[k] = true
				columns = append(columns, k)
			}
		}
	}
	return columns
}

// csvRecord renders one row against the column list.
func csvRecord(row any, columns []string) ([]string, error) {
	switch t := row.(type) {
	case map[string]any:
		record := make([]string, 0, len(columns))
		for _, name := range columns {
			value, ok := t[name]
			if !ok {
				record = append(record, "")
				continue
			}
			record = append(record, stringify(value))
		}
		return record, nil

	case []any:
		record := make([]string, 0, len(t))
		for _, field := range t {
			record = append(record, stringify(field))
		}
		return record, nil

	default:
		return nil, fmt.Errorf("need an object or an array, got %T", row)
	}
}
