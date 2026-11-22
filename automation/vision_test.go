package automation

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImageMatchGeometry pins same-scale scoring, overlap suppression, and image-relative
// coordinates when captures and references have nonzero Go image origins.
func TestImageMatchGeometry(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(10, 20, 12, 22))
	colors := []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}}
	for i, c := range colors {
		reference.SetNRGBA(10+i%2, 20+i/2, c)
	}
	img := image.NewNRGBA(image.Rect(-5, -7, 5, 1))
	for y := -7; y < 1; y++ {
		for x := -5; x < 5; x++ {
			img.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}
	for _, origin := range []image.Point{{-3, -4}, {1, -1}} {
		for i, c := range colors {
			img.SetNRGBA(origin.X+i%2, origin.Y+i/2, c)
		}
	}
	result, err := MatchImage(context.Background(), img, reference, ImageMatchOptions{MinimumMatch: 100, MaxResults: 10})
	require.NoError(t, err)
	assert.Equal(t, []ImageMatch{{X: 2, Y: 3, Width: 2, Height: 2, MatchPercent: 100}, {X: 6, Y: 6, Width: 2, Height: 2, MatchPercent: 100}}, result.Matches)
	assert.False(t, result.Truncated)
	result, err = MatchImage(context.Background(), img, reference, ImageMatchOptions{MinimumMatch: 100, MaxResults: 1})
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Len(t, result.Matches, 1)
	reference.SetNRGBA(10, 20, color.NRGBA{R: 252, A: 255})
	result, err = MatchImage(context.Background(), img, reference, ImageMatchOptions{MinimumMatch: 99.9})
	require.NoError(t, err)
	require.Len(t, result.Matches, 2)
	assert.InDelta(t, 100*(1-3.0/(4*3*255)), result.Matches[0].MatchPercent, 1e-9)
	result, err = MatchImage(context.Background(), img, reference, ImageMatchOptions{MinimumMatch: 100})
	require.NoError(t, err)
	assert.False(t, result.Matched)
	flat := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			flat.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	result, err = MatchImage(context.Background(), flat, flat.SubImage(image.Rect(0, 0, 2, 2)), ImageMatchOptions{MinimumMatch: 100})
	require.NoError(t, err)
	// Adjacent 2x2 detections overlap by 2 / (4+4-2), above the 0.3 merge threshold.
	assert.Len(t, result.Matches, 1)

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, reference))
	decoded, err := DecodeImage(encoded.Bytes())
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 2, 2), decoded.Bounds())
	_, err = DecodeImage(encoded.Bytes()[:15])
	assert.Error(t, err)
}

// TestImageMatchWorkBudget pins the worst case where nearly identical flat pixels
// postpone rejection; an incomplete scan must never report a successful no-match.
func TestImageMatchWorkBudget(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	reference := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetNRGBA(x, y, color.NRGBA{128, 128, 128, 255})
		}
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			c := uint8(127 + 2*((x+y)%2))
			reference.SetNRGBA(x, y, color.NRGBA{c, c, c, 255})
		}
	}
	_, err := MatchImage(context.Background(), img, reference, ImageMatchOptions{MinimumMatch: 99.7})
	assert.ErrorContains(t, err, "pixel comparisons")
	cropped := img.SubImage(image.Rect(0, 0, 100, 100))
	result, err := MatchImage(context.Background(), cropped, reference, ImageMatchOptions{MinimumMatch: 99.7})
	require.NoError(t, err)
	assert.False(t, result.Matched)
}

// TestCaptureMappingKeepsNormalizedOrigins covers independent scale axes and negative desktop origins after PNG normalization.
func TestCaptureMappingKeepsNormalizedOrigins(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 110, 70))
	mapping := &CaptureMapping{OriginX: -120, OriginY: 80, UnitsPerPixelX: 0.5, UnitsPerPixelY: 2}
	mapped := autoapi.WithCaptureMapping(img, mapping)
	result := ImageMatchResult{Matches: []ImageMatch{{X: 20, Y: 10, Width: 8, Height: 6}}}
	geometry := MapImageMatches(mapped, &result)
	assert.True(t, geometry.MappingKnown)
	assert.Equal(t, &DesktopBounds{X: -120, Y: 80, Width: 50, Height: 100}, geometry.Bounds)
	assert.Equal(t, &DesktopBounds{X: -110, Y: 100, Width: 4, Height: 12}, result.Matches[0].DesktopBounds)
	assert.Equal(t, &DesktopPoint{X: -108, Y: 106}, result.Matches[0].DesktopCenter)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, mapped))
	decoded, err := DecodeImage(encoded.Bytes())
	require.NoError(t, err)
	assert.False(t, ImageCaptureGeometry(decoded).MappingKnown, "binary input must not invent a desktop mapping")
	assert.Equal(t, geometry, ImageCaptureGeometry(autoapi.WithCaptureMapping(decoded, mapping)), "PNG helper geometry must retain normalized origin despite changing Go bounds")
}
