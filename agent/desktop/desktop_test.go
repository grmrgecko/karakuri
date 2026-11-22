package desktop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// mediaTestContext serves a fixed configuration and renders templates as
// written.
type mediaTestContext struct {
	nodeapi.ExecContext
	config string
}

func (c mediaTestContext) Config(v any) error { return json.Unmarshal([]byte(c.config), v) }
func (mediaTestContext) Interpolate(s string, _ *nodeapi.Msg) (string, error) {
	return s, nil
}

// recordedMedia keeps the requests a media node sent to the session.
type recordedMedia struct {
	requests []automation.MediaRequest
}

func (m *recordedMedia) Control(_ context.Context, req automation.MediaRequest) error {
	m.requests = append(m.requests, req)
	return nil
}

// TestMediaVolumeAbsentFieldDefault verifies Set volume uses the level the
// editor shows when a saved configuration omits it. It pins the regression
// where the omitted field decoded as zero and silenced the host, while an
// explicit zero still has to mute.
func TestMediaVolumeAbsentFieldDefault(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		volume       float64
	}{
		{"omitted volume", `{"action":"volume_set"}`, 50},
		{"explicit zero", `{"action":"volume_set","volume":0}`, 0},
		{"explicit level", `{"action":"volume_set","volume":80}`, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			media := &recordedMedia{}
			node := &mediaNode{sess: &automation.Session{Media: media}}

			_, err := node.Execute(context.Background(), mediaTestContext{config: tc.config}, map[string]*nodeapi.Msg{"in": {}})
			require.NoError(t, err, "a volume inside 0 to 100 is valid")
			require.Len(t, media.requests, 1, "one action reaches the session")
			assert.Equal(t, tc.volume, media.requests[0].Volume, "an omitted volume is the default, and a written one is kept")
		})
	}
}

// recordedOpener keeps the arguments an open node handed the session.
type recordedOpener struct {
	args []string
}

func (o *recordedOpener) OpenURL(context.Context, string) error { return nil }
func (o *recordedOpener) OpenApp(_ context.Context, _ string, args []string) error {
	o.args = args
	return nil
}

// TestOpenAppArgumentsKeepSurroundingWhitespace verifies Open Application
// passes each argument as written, as Run Command does. It pins the regression
// where each argument was trimmed, so a lone space given as a delimiter
// arrived as an empty string and a title lost its indentation and line break.
func TestOpenAppArgumentsKeepSurroundingWhitespace(t *testing.T) {
	opener := &recordedOpener{}
	node := &openAppNode{sess: &automation.Session{Opener: opener}}
	config := `{"app":"notes","args":["-d"," ","--title","  indented note\n"]}`

	_, err := node.Execute(context.Background(), mediaTestContext{config: config}, map[string]*nodeapi.Msg{"in": {}})
	require.NoError(t, err, "an application with arguments must open")
	assert.Equal(t, []string{"-d", " ", "--title", "  indented note\n"}, opener.args,
		"whitespace inside an argument is part of the argument")
}
