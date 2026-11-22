package agentnode

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// PassThrough builds the outgoing message for a node that acts on the world and
// hands its input along, with the outcome added to metadata.
func PassThrough(msg *nodeapi.Msg, meta map[string]any) *nodeapi.Msg {
	out := &nodeapi.Msg{
		ID:      msg.ID,
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	for k, v := range meta {
		out.Meta[k] = v
	}
	return out
}

// ResolveProperty reads a dot path from a message: payload, topic, meta, or a
// path inside any of them. An unqualified path is read from the payload, which
// is what "payload.user.name" and "user.name" both mean to a flow author.
func ResolveProperty(msg *nodeapi.Msg, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "payload" {
		return msg.Payload, true
	}
	switch {
	case path == "topic":
		return msg.Topic, true
	case path == "meta":
		return msg.Meta, true
	case strings.HasPrefix(path, "meta."):
		return walkValue(msg.Meta, strings.Split(path[len("meta."):], "."))
	case strings.HasPrefix(path, "payload."):
		path = path[len("payload."):]
	}
	return walkValue(msg.Payload, strings.Split(path, "."))
}

// walkValue descends a decoded value one path segment at a time, indexing into
// arrays by number.
func walkValue(value any, segments []string) (any, bool) {
	cur := value
	for _, segment := range segments {
		switch t := cur.(type) {
		case map[string]any:
			next, ok := t[segment]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(t) {
				return nil, false
			}
			cur = t[index]
		default:
			return nil, false
		}
	}
	return cur, true
}

// ValueText renders a value as the text an OS tool is handed. Bytes go through
// as the characters they are, and anything structured is JSON, so a tool never
// receives Go's own formatting of a map.
func ValueText(value any) string {
	switch t := value.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

// ValueBytes renders a value as the bytes written to a file. Binary is written
// as it is; everything else follows ValueText.
func ValueBytes(value any) []byte {
	if b, ok := value.([]byte); ok {
		return b
	}
	return []byte(ValueText(value))
}

// Interpolate renders {{ }} segments in a config value against the message.
func Interpolate(ec nodeapi.ExecContext, raw string, msg *nodeapi.Msg) (string, error) {
	rendered, err := ec.Interpolate(strings.TrimSpace(raw), msg)
	if err != nil {
		return "", fmt.Errorf("invalid template %q: %w", raw, err)
	}
	return rendered, nil
}

// Path renders a configured path and makes it absolute. There is no root policy
// to check it against: the agent's own user is the boundary, so a path the
// account cannot reach fails at the syscall with the reason the kernel gives
// rather than at a config check that would have to be maintained twice.
func Path(ec nodeapi.ExecContext, raw string, msg *nodeapi.Msg) (string, error) {
	rendered, err := Interpolate(ec, raw, msg)
	if err != nil {
		return "", err
	}
	if rendered == "" {
		return "", errors.New("no file path configured")
	}
	abs, err := filepath.Abs(rendered)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", rendered, err)
	}
	return filepath.Clean(abs), nil
}

// PrimaryInput picks the message a node acts on: the main port when it carries
// one, otherwise whichever port did. A node with several inputs still has one
// message it is about, and this is the agreement on which.
func PrimaryInput(inputs map[string]*nodeapi.Msg) *nodeapi.Msg {
	if m, ok := inputs["in"]; ok && m != nil {
		return m
	}
	for _, m := range inputs {
		if m != nil {
			return m
		}
	}
	return &nodeapi.Msg{}
}
