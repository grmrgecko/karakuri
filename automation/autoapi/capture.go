package autoapi

import (
	"image"
	"math"
)

// CaptureMapping locates normalized image pixel (0,0) in desktop input coordinates.
// Each scale is desktop units per image pixel, not a display's configured DPI.
type CaptureMapping struct {
	OriginX        float64 `json:"origin_x"`
	OriginY        float64 `json:"origin_y"`
	UnitsPerPixelX float64 `json:"units_per_pixel_x"`
	UnitsPerPixelY float64 `json:"units_per_pixel_y"`
}

type mappedCapture struct {
	image.Image
	mapping CaptureMapping
}

// CaptureMapping returns provider-authored geometry for this capture.
func (c mappedCapture) CaptureMapping() CaptureMapping { return c.mapping }

// WithCaptureMapping attaches reliable provider geometry without changing image pixels.
// Invalid geometry is omitted so callers cannot act on invented coordinates.
func WithCaptureMapping(img image.Image, mapping *CaptureMapping) image.Image {
	if img == nil || mapping == nil {
		return img
	}
	for _, value := range []float64{mapping.OriginX, mapping.OriginY, mapping.UnitsPerPixelX, mapping.UnitsPerPixelY} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return img
		}
	}
	if mapping.UnitsPerPixelX <= 0 || mapping.UnitsPerPixelY <= 0 {
		return img
	}
	return mappedCapture{Image: img, mapping: *mapping}
}

// ImageCaptureMapping reads optional geometry whose origin is independent of Go image bounds.
func ImageCaptureMapping(img image.Image) *CaptureMapping {
	provider, ok := img.(interface{ CaptureMapping() CaptureMapping })
	if !ok {
		return nil
	}
	mapping := provider.CaptureMapping()
	return &mapping
}
