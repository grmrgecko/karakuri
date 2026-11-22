package gui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// keyboardTestContext serves a fixed configuration and renders templates as
// written.
type keyboardTestContext struct {
	nodeapi.ExecContext
	config string
}

func (c keyboardTestContext) Config(v any) error { return json.Unmarshal([]byte(c.config), v) }
func (keyboardTestContext) Interpolate(s string, _ *nodeapi.Msg) (string, error) {
	return s, nil
}
func (keyboardTestContext) Clock() nodeapi.Clock { return nodeapi.RealClock{} }

// recordedKeyboard keeps the text a keyboard node asked the session to type.
type recordedKeyboard struct {
	typed []string
}

func (k *recordedKeyboard) TypeText(_ context.Context, _ autoapi.Clock, text string, _ time.Duration) error {
	k.typed = append(k.typed, text)
	return nil
}

func (*recordedKeyboard) PressKeys(context.Context, autoapi.KeyStroke) error { return nil }

// TestKeyboardTypesTextAsWritten verifies Type text sends the configured text
// with its leading and trailing whitespace. It pins the regression where the
// text was trimmed first, so a command ending in a line break was typed
// without the Return that submits it.
func TestKeyboardTypesTextAsWritten(t *testing.T) {
	for _, text := range []string{"echo ready\n", "  indented", "name\t"} {
		t.Run(text, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"action": "type", "text": text})
			require.NoError(t, err, "the fixture configuration must encode")
			keyboard := &recordedKeyboard{}
			node := &keyboardNode{sess: &automation.Session{Keyboard: keyboard}}

			_, err = node.Execute(context.Background(), keyboardTestContext{config: string(raw)}, map[string]*nodeapi.Msg{"in": {}})
			require.NoError(t, err, "typing with no window target needs no focus check")
			assert.Equal(t, []string{text}, keyboard.typed, "whitespace at either end is part of what is typed")
		})
	}
}
