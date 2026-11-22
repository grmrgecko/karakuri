package agentnode

import (
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// WithResult copies a message and stores a result without mutating shared ancestors.
func WithResult(msg *nodeapi.Msg, path string, value any) (*nodeapi.Msg, error) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) < 2 || (parts[0] != "payload" && parts[0] != "meta") {
		return nil, fmt.Errorf("result property must name a payload or metadata descendant")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("result property contains an empty segment")
		}
	}
	out := PassThrough(msg, nil)
	var root any = out.Payload
	if parts[0] == "meta" {
		root = out.Meta
	}
	updated, err := resultObject(root, parts[1:], value)
	if err != nil {
		return nil, err
	}
	if parts[0] == "meta" {
		out.Meta = updated
	} else {
		out.Payload = updated
	}
	return out, nil
}

func resultObject(root any, parts []string, value any) (map[string]any, error) {
	obj, ok := root.(map[string]any)
	if root != nil && !ok {
		return nil, fmt.Errorf("result property parent must be an object")
	}
	copy := make(map[string]any, len(obj)+1)
	for k, v := range obj {
		copy[k] = v
	}
	if len(parts) == 1 {
		copy[parts[0]] = value
		return copy, nil
	}
	next, err := resultObject(obj[parts[0]], parts[1:], value)
	if err != nil {
		return nil, err
	}
	copy[parts[0]] = next
	return copy, nil
}
