package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// stateKeyField selects an independent state bucket from a message property.
func stateKeyField() engine.FieldSpec {
	return engine.FieldSpec{ID: "key", Label: "Track separately by", Type: flow.FieldString, Placeholder: "topic", Help: "Message property identifying an independent timer or state, such as topic or payload.device_id. Empty shares one state."}
}

// messageStateKey preserves JSON value types so numeric and textual IDs differ.
func messageStateKey(msg *engine.Msg, property string) (string, error) {
	property = strings.TrimSpace(property)
	if property == "" {
		return "", nil
	}
	value, ok := resolveProperty(msg, property)
	if !ok {
		return "", fmt.Errorf("key property %q is not present on the message", property)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("key property %q: %w", property, err)
	}
	return string(raw), nil
}

// keyedStoreKey retains the legacy unkeyed entry while isolating keyed state.
func keyedStoreKey(base, key string) string {
	if key == "" {
		return base
	}
	return base + ":key:" + key
}

// storableText returns text in a form a durable store hands back unchanged.
// The store keeps JSON, which rewrites bytes that are not valid UTF-8, so a
// value compared against its stored copy would never match. Such text is
// replaced by a digest of its bytes, which equal inputs still share.
func storableText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}
