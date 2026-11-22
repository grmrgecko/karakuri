// Package scriptsdk derives script function catalogs and validates calls made
// through them. It does not execute nodes or integrations.
package scriptsdk

import "github.com/grmrgecko/karakuri/pkg/flow"

// SchemaVersion is the function-catalog response format version.
const SchemaVersion = 1

// Function describes one executable node function. Name is the exact node type.
// Aliases contains only unambiguous convenience names.
type Function struct {
	Name             string        `json:"name"`
	Aliases          []string      `json:"aliases,omitempty"`
	NodeType         string        `json:"node_type"`
	IdentityRequired bool          `json:"identity_required"`
	Meta             flow.NodeMeta `json:"meta"`
}

// Catalog is a deterministic snapshot of the active function registry.
type Catalog struct {
	SchemaVersion int        `json:"schema_version"`
	Revision      string     `json:"revision"`
	Functions     []Function `json:"functions"`
}

// Diagnostic describes one static script verification finding. From and To are
// byte offsets in the original source; Line and Column are one-based.
type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	NodeType string `json:"node_type,omitempty"`
}
