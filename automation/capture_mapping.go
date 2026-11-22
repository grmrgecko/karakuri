package automation

import (
	"image"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// DesktopBounds describes a capture or match in desktop input coordinates.
type DesktopBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// DesktopPoint describes a point in desktop input coordinates.
type DesktopPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// CaptureGeometry reports whether image pixels have a reliable desktop mapping.
type CaptureGeometry struct {
	MappingKnown bool            `json:"mapping_known"`
	Mapping      *CaptureMapping `json:"desktop_mapping,omitempty"`
	Bounds       *DesktopBounds  `json:"desktop_bounds,omitempty"`
}

// ImageCaptureGeometry returns optional provider-authored normalized-image geometry.
func ImageCaptureGeometry(img image.Image) CaptureGeometry {
	mapping := autoapi.ImageCaptureMapping(img)
	geometry := CaptureGeometry{MappingKnown: mapping != nil, Mapping: mapping}
	if mapping != nil {
		bounds := mappedBounds(mapping, 0, 0, img.Bounds().Dx(), img.Bounds().Dy())
		geometry.Bounds = &bounds
	}
	return geometry
}

func mappedBounds(mapping *CaptureMapping, x, y, width, height int) DesktopBounds {
	return DesktopBounds{X: mapping.OriginX + float64(x)*mapping.UnitsPerPixelX, Y: mapping.OriginY + float64(y)*mapping.UnitsPerPixelY, Width: float64(width) * mapping.UnitsPerPixelX, Height: float64(height) * mapping.UnitsPerPixelY}
}

// MapImageMatches adds desktop coordinates only when the source reports reliable mapping.
func MapImageMatches(img image.Image, result *ImageMatchResult) CaptureGeometry {
	geometry := ImageCaptureGeometry(img)
	if !geometry.MappingKnown {
		return geometry
	}
	for i := range result.Matches {
		match := &result.Matches[i]
		bounds := mappedBounds(geometry.Mapping, match.X, match.Y, match.Width, match.Height)
		match.DesktopBounds = &bounds
		match.DesktopCenter = &DesktopPoint{X: bounds.X + bounds.Width/2, Y: bounds.Y + bounds.Height/2}
	}
	return geometry
}
