package render

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// TestConvertFromPNG checks that every format a render URL may ask for comes
// back as a file its own standard decoder reads. The browser only ever hands
// back a PNG, so everything but PNG goes through this re-encoding, and a photo
// frame that will only take BMP or an e-ink panel that wants TIFF is the whole
// reason the other formats exist. A file none of them can open is a format we
// should not be offering.
func TestConvertFromPNG(t *testing.T) {
	// A 3x2 image with distinguishable corners, so a conversion that loses the
	// geometry shows up as a size or a bounds mismatch rather than as nothing.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{R: 255, A: 255})
	src.Set(2, 1, color.RGBA{B: 255, A: 255})

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, src), "the fixture stands in for what the browser returns")
	shot := encoded.Bytes()

	decoders := map[string]func([]byte) (image.Image, error){
		"png":  func(b []byte) (image.Image, error) { return png.Decode(bytes.NewReader(b)) },
		"jpg":  func(b []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(b)) },
		"gif":  func(b []byte) (image.Image, error) { return gif.Decode(bytes.NewReader(b)) },
		"bmp":  func(b []byte) (image.Image, error) { return bmp.Decode(bytes.NewReader(b)) },
		"tiff": func(b []byte) (image.Image, error) { return tiff.Decode(bytes.NewReader(b)) },
	}

	for _, format := range Formats {
		// PDF is printed by the browser rather than converted here.
		if format == "pdf" {
			continue
		}

		out, err := convertFromPNG(shot, format)
		require.NoErrorf(t, err, "a render must be encodable as %s", format)

		img, err := decoders[format](out)
		require.NoErrorf(t, err, "the %s a render produces must decode as %s", format, format)
		assert.Equalf(t, src.Bounds(), img.Bounds(), "%s must keep the rendered size", format)
	}

	_, err := convertFromPNG([]byte("not an image"), "jpg")
	assert.Error(t, err, "bytes that are not a screenshot must fail rather than produce a broken file")

	_, err = convertFromPNG(shot, "webp")
	assert.Error(t, err, "a format this build cannot encode must be refused")
}
