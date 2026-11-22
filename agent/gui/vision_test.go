package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type visionTestContext struct {
	nodeapi.ExecContext
	config string
}

func (c visionTestContext) Config(v any) error { return json.Unmarshal([]byte(c.config), v) }

// TestImageMatchDefaultMinimum verifies a configuration that omits the minimum
// match searches at the documented 98 percent. It pins the regression where
// the omitted field reached the matcher as zero and failed every such node
// with an out-of-range error.
func TestImageMatchDefaultMinimum(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			source.Set(x, y, color.RGBA{A: 255})
		}
	}
	reference := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			shade := color.RGBA{R: uint8(200 + x*10), G: uint8(40 * y), B: 255, A: 255}
			reference.Set(x, y, shade)
			source.Set(6+x, 9+y, shade)
		}
	}
	encode := func(img image.Image) []byte {
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, img))
		return buf.Bytes()
	}
	raw, err := json.Marshal(map[string]any{
		"capture": "input", "image": encode(source),
		"reference_source": "input", "reference": encode(reference),
	})
	require.NoError(t, err)

	n := &imageMatchNode{}
	out, err := n.Execute(context.Background(), visionTestContext{config: string(raw)}, nil)
	require.NoError(t, err, "an omitted minimum match must fall back to its default instead of failing validation")

	encoded, err := json.Marshal(out["out"][0].Payload)
	require.NoError(t, err)
	var result struct {
		Matches []struct {
			X int `json:"x"`
			Y int `json:"y"`
		} `json:"matches"`
	}
	require.NoError(t, json.Unmarshal(encoded, &result))
	require.Len(t, result.Matches, 1, "the identical patch is the only region at or above the default threshold")
	assert.Equal(t, 6, result.Matches[0].X, "the match must sit at the patch's left edge")
	assert.Equal(t, 9, result.Matches[0].Y, "the match must sit at the patch's top edge")
}
