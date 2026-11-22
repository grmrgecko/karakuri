package mcpserver

import (
	"encoding/json"
	"errors"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// toGraph reads a tool argument as a flow graph document. Tool inputs carry
// graphs as plain JSON values rather than flow.Graph, because the SDK infers a
// JSON schema from the input struct and flow.Graph is recursive (a graph holds
// subgraphs), which schema inference rejects. A graph arriving as a string is
// read as the JSON encoding of the document: a string is never a valid graph,
// and MCP clients routinely stringify parameters the schema leaves untyped.
func toGraph(v any) (*flow.Graph, error) {
	if v == nil {
		return nil, errors.New("graph is required")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if s, ok := v.(string); ok {
		raw = []byte(s)
	}
	var g flow.Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, errors.New("graph does not parse as a flow graph document: " + err.Error())
	}
	return &g, nil
}

// docJSON reads a tool argument holding a JSON document object, accepting the
// object itself or its JSON encoding as a string, for the same stringifying
// clients toGraph tolerates. Returns nil for an absent value.
func docJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		var probe any
		if err := json.Unmarshal([]byte(s), &probe); err != nil {
			return nil, errors.New("document does not parse as JSON: " + err.Error())
		}
		return json.RawMessage(s), nil
	}
	return json.Marshal(v)
}

// rawJSON re-encodes a tool argument for a caller that takes json.RawMessage,
// returning nil for an absent value.
func rawJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
