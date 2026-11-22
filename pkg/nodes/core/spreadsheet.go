package core

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/xuri/excelize/v2"
)

// SpreadsheetNode reads an xlsx workbook into rows.
//
// It is the csv node for the format people actually email each other. A meter
// export, an energy bill, a parts list: they arrive as xlsx, and the alternative
// today is asking the sender to save as CSV, which is a thing a flow cannot do.
//
// Rows come back the way the csv node's do, as objects keyed by column, so
// everything downstream that already handles CSV handles this without knowing
// which one it got.
type SpreadsheetNode struct{}

func (SpreadsheetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "spreadsheet",
		Label:       "Spreadsheet",
		Folder:      flow.FolderParsers,
		Description: "Reads an xlsx workbook into rows",
		Icon:        "las la-file-excel",
		Inputs:      converterInput(flow.PortBinary),
		Outputs:     converterOutput(flow.PortObject),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:          "sheet",
				Label:       "Sheet",
				Type:        flow.FieldString,
				Placeholder: "Readings",
				Help:        "Sheet name. Empty reads the workbook's first sheet.",
			},
			{
				ID:      "header",
				Label:   "Header row",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "The first row holds the column names. Off keeps rows as arrays of cells.",
			},
			{
				ID:          "columns",
				Label:       "Columns",
				Type:        flow.FieldString,
				Placeholder: "date,kwh,cost",
				ShowIf:      &flow.ShowIf{Field: "header", Equals: "false"},
				Help:        "Comma-separated names for the columns when there is no header row.",
			},
			{
				ID:      "output",
				Label:   "Read into",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "One message holding every row"},
					{Value: "messages", Label: "A message per row"},
				},
				Help: "A message per row fans out like a split, so a join downstream can put the rows back together.",
			},
			{
				ID:      "parse_numbers",
				Label:   "Read numeric cells as numbers",
				Type:    flow.FieldBoolean,
				Default: true,
				Help: "Off keeps every cell as the text the sheet displays, which preserves leading " +
					"zeros and the formatting of a date column.",
			},
		},
	}
}

// spreadsheetConfig is the decoded spreadsheet node configuration.
type spreadsheetConfig struct {
	Property     string `json:"property"`
	Target       string `json:"target"`
	Sheet        string `json:"sheet"`
	Header       *bool  `json:"header"`
	Columns      string `json:"columns"`
	Output       string `json:"output"`
	ParseNumbers *bool  `json:"parse_numbers"`
}

// Execute reads the workbook.
func (SpreadsheetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg spreadsheetConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	data, err := archiveBytes(value, property)
	if err != nil {
		return nil, err
	}

	// Open the workbook and pick the sheet.
	// A workbook is a zip. Without these limits excelize inflates up to 16 GB
	// and spills XML past 16 MB into temp files, so a small hostile file could
	// fill the disk before a row is read.
	book, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    maxArchiveBytes,
		UnzipXMLSizeLimit: maxArchiveBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the workbook: %w", err)
	}
	defer book.Close()

	sheet := strings.TrimSpace(cfg.Sheet)
	if sheet == "" {
		names := book.GetSheetList()
		if len(names) == 0 {
			return nil, fmt.Errorf("the workbook has no sheets")
		}
		sheet = names[0]
	}
	rows, err := book.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("reading sheet %q: %w", sheet, err)
	}

	// Resolve the column names, then build one object per remaining row.
	header := cfg.Header == nil || *cfg.Header
	names, body, err := spreadsheetColumns(rows, header, cfg.Columns)
	if err != nil {
		return nil, err
	}
	numbers := cfg.ParseNumbers == nil || *cfg.ParseNumbers
	items := make([]any, 0, len(body))
	for _, row := range body {
		items = append(items, spreadsheetRow(row, names, numbers))
	}

	if cfg.Output == "messages" {
		return parserFanOut(msg, property, cfg.Target, items)
	}
	out, err := parserResult(msg, property, cfg.Target, items)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// spreadsheetColumns resolves the column names and the rows that hold data.
// Without a header the names may be configured, and anything past them is keyed
// by position so a wide row is not silently truncated.
func spreadsheetColumns(rows [][]string, header bool, configured string) ([]string, [][]string, error) {
	if header {
		if len(rows) == 0 {
			return nil, nil, fmt.Errorf("the sheet is empty, so it has no header row")
		}
		return rows[0], rows[1:], nil
	}
	var names []string
	for _, name := range strings.Split(configured, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names, rows, nil
}

// spreadsheetRow turns one row of cells into an object.
func spreadsheetRow(row, names []string, numbers bool) map[string]any {
	out := make(map[string]any, len(row))
	for i, cell := range row {
		key := fmt.Sprintf("column_%d", i+1)
		if i < len(names) && strings.TrimSpace(names[i]) != "" {
			key = strings.TrimSpace(names[i])
		}
		out[key] = spreadsheetCell(cell, numbers)
	}
	// A trailing empty cell is omitted by the reader rather than returned, so a
	// short row would otherwise be missing keys the rows around it have.
	for i := len(row); i < len(names); i++ {
		if key := strings.TrimSpace(names[i]); key != "" {
			out[key] = ""
		}
	}
	return out
}

// spreadsheetCell reads a cell as a number when asked and when it is one.
func spreadsheetCell(cell string, numbers bool) any {
	if !numbers {
		return cell
	}
	trimmed := strings.TrimSpace(cell)
	if trimmed == "" {
		return cell
	}
	// ParseFloat also reads "NaN" and "Inf", which are words in a sheet and
	// floats JSON cannot encode.
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return n
	}
	return cell
}

// SpreadsheetEncodeNode writes rows out as an xlsx workbook.
type SpreadsheetEncodeNode struct{}

func (SpreadsheetEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "spreadsheet-encode",
		Label:       "Spreadsheet Encode",
		Folder:      flow.FolderEncoders,
		Description: "Writes rows out as an xlsx workbook",
		Icon:        "las la-file-excel",
		// Rows of fields, so there has to be an array of objects to write.
		Inputs:  converterInput(flow.PortObject),
		Outputs: converterOutput(flow.PortBinary),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:          "sheet",
				Label:       "Sheet name",
				Type:        flow.FieldString,
				Default:     "Sheet1",
				Placeholder: "Readings",
			},
			{
				ID:          "columns",
				Label:       "Columns",
				Type:        flow.FieldString,
				Placeholder: "date,kwh,cost",
				Help: "Comma-separated field names, in the order the columns should appear. " +
					"Empty takes them from the first row, sorted, so the output is at least stable.",
			},
			{
				ID:      "header",
				Label:   "Write a header row",
				Type:    flow.FieldBoolean,
				Default: true,
			},
		},
	}
}

// spreadsheetEncodeConfig is the decoded spreadsheet-encode configuration.
type spreadsheetEncodeConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	Sheet    string `json:"sheet"`
	Columns  string `json:"columns"`
	Header   *bool  `json:"header"`
}

// Execute writes the rows to a workbook.
func (SpreadsheetEncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg spreadsheetEncodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	rows, err := tabularRows(value, property)
	if err != nil {
		return nil, err
	}
	columns := tabularColumns(cfg.Columns, rows)

	// Build the workbook. excelize starts every file with Sheet1, so a named
	// sheet renames that one rather than leaving an empty one beside it.
	book := excelize.NewFile()
	defer book.Close()
	sheet := strings.TrimSpace(cfg.Sheet)
	if sheet == "" {
		sheet = "Sheet1"
	}
	if sheet != "Sheet1" {
		if err := book.SetSheetName("Sheet1", sheet); err != nil {
			return nil, fmt.Errorf("naming the sheet %q: %w", sheet, err)
		}
	}

	line := 1
	if cfg.Header == nil || *cfg.Header {
		header := make([]any, len(columns))
		for i, name := range columns {
			header[i] = name
		}
		if err := writeSheetRow(book, sheet, line, header); err != nil {
			return nil, err
		}
		line++
	}
	for _, row := range rows {
		cells := make([]any, len(columns))
		for i, name := range columns {
			cells[i] = row[name]
		}
		if err := writeSheetRow(book, sheet, line, cells); err != nil {
			return nil, err
		}
		line++
	}

	var buf bytes.Buffer
	if err := book.Write(&buf); err != nil {
		return nil, fmt.Errorf("writing the workbook: %w", err)
	}
	out, err := parserResult(msg, property, cfg.Target, buf.Bytes())
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// writeSheetRow writes one row starting at column A.
func writeSheetRow(book *excelize.File, sheet string, line int, cells []any) error {
	origin, err := excelize.CoordinatesToCellName(1, line)
	if err != nil {
		return err
	}
	if err := book.SetSheetRow(sheet, origin, &cells); err != nil {
		return fmt.Errorf("writing row %d: %w", line, err)
	}
	return nil
}

// tabularRows reads the value as rows of an object each, accepting one object
// as a single row so a flow writing one reading does not have to wrap it.
func tabularRows(value any, property string) ([]map[string]any, error) {
	switch t := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(t))
		for i, item := range t {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("row %d is %T, not an object", i, item)
			}
			out = append(out, obj)
		}
		return out, nil
	case map[string]any:
		return []map[string]any{t}, nil
	default:
		return nil, fmt.Errorf("property %q is %T, not rows to write", property, value)
	}
}

// tabularColumns resolves the column order: the configured list, or the first
// row's keys sorted, so the same data writes the same file twice running.
func tabularColumns(configured string, rows []map[string]any) []string {
	var columns []string
	for _, name := range strings.Split(configured, ",") {
		if name = strings.TrimSpace(name); name != "" {
			columns = append(columns, name)
		}
	}
	if len(columns) > 0 || len(rows) == 0 {
		return columns
	}
	for name := range rows[0] {
		columns = append(columns, name)
	}
	sort.Strings(columns)
	return columns
}
