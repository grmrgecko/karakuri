package main

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStateOptions pins command-file syntax, including literal Windows evidence
// paths and window-relative crops, before a command can reach the desktop.
func TestStateOptions(t *testing.T) {
	for _, tc := range []struct {
		line    string
		want    stateOptions
		invalid bool
	}{
		{line: `state --window Setup --text Next --enabled --timeout 1.5 --evidence "C:\Users\James\captures"`, want: stateOptions{window: "Setup", text: "Next", enabled: true, timeout: 1500 * time.Millisecond, evidence: `C:\Users\James\captures`}},
		{line: `state --window 0x123 --role AXSheet --gone`, want: stateOptions{window: "0x123", role: "AXSheet", gone: true, timeout: 30 * time.Second}},
		{line: `state --window Setup --stable 0.5 --region 10,20,300,100`, want: stateOptions{window: "Setup", stable: 500 * time.Millisecond, timeout: 30 * time.Second, region: image.Rect(10, 20, 310, 120)}},
		{line: `state --window Setup --stable 1 --text Next`, invalid: true},
		{line: `state --window Setup --region 0,0,10,10 --text Next`, invalid: true},
		{line: `state --window Setup --stable 1 --region -1,0,10,10`, invalid: true},
		{line: `state --window Setup --text Next --timeout NaN`, invalid: true},
		{line: `state --window Setup --text Next --timeout 0`, invalid: true},
		{line: `state --window Setup --stable 30`, invalid: true},
		{line: `state --window Setup --text Next --text Back`, invalid: true},
		{line: `state --text Next`, invalid: true},
	} {
		t.Run(tc.line, func(t *testing.T) {
			args, err := commandWords(tc.line)
			require.NoError(t, err)
			got, err := parseStateOptions(args)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestStateImageDigest pins comparisons across capture backends whose images
// can have different origins, row strides, or color models.
func TestStateImageDigest(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 2; y < 4; y++ {
		for x := 3; x < 6; x++ {
			full.Set(x, y, color.RGBA{R: 12, G: 34, B: 56, A: 255})
		}
	}
	crop := full.SubImage(image.Rect(3, 2, 6, 4))
	copy := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			copy.Set(x, y, crop.At(x+3, y+2))
		}
	}
	assert.Equal(t, stateImageDigest(crop), stateImageDigest(copy), "pixel comparisons must ignore storage layout")
	resized := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			resized.Set(x, y, copy.At(0, 0))
		}
	}
	assert.NotEqual(t, stateImageDigest(copy), stateImageDigest(resized), "a resize must restart the stability interval")
	copy.Set(1, 1, color.Black)
	assert.NotEqual(t, stateImageDigest(crop), stateImageDigest(copy), "a changed pixel must restart the stability interval")
}
