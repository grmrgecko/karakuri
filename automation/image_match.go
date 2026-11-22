package automation

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
)

const maxMatchComparisons = 100000000
const maxReferencePixels = 1 << 20

// ImageMatchOptions sets RGB agreement and a limit on distinct image regions.
type ImageMatchOptions struct {
	MinimumMatch float64 `json:"minimum_match"`
	MaxResults   int     `json:"max_results"`
}

// ImageMatch reports image-pixel bounds and mean RGB agreement as a percentage.
type ImageMatch struct {
	X             int            `json:"x"`
	Y             int            `json:"y"`
	Width         int            `json:"width"`
	Height        int            `json:"height"`
	MatchPercent  float64        `json:"match_percent"`
	DesktopBounds *DesktopBounds `json:"desktop_bounds,omitempty"`
	DesktopCenter *DesktopPoint  `json:"desktop_center,omitempty"`
}

// ImageMatchResult distinguishes a completed no-match scan from a result cap.
type ImageMatchResult struct {
	CoordinateSpace string       `json:"coordinate_space"`
	Width           int          `json:"width"`
	Height          int          `json:"height"`
	Matched         bool         `json:"matched"`
	Matches         []ImageMatch `json:"matches"`
	Truncated       bool         `json:"truncated"`
}

type rgbPixel struct{ r, g, b uint8 }
type referencePixel struct {
	x, y     int
	rgb      rgbPixel
	contrast int
}

func rgbImage(ctx context.Context, img image.Image, maxPixels int, opaque bool) ([]rgbPixel, error) {
	if img == nil {
		return nil, fmt.Errorf("image is required")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > maxPixels/h {
		return nil, fmt.Errorf("image exceeds %d pixels or has invalid dimensions", maxPixels)
	}
	pixels := make([]rgbPixel, w*h)
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if opaque && c.A != 255 {
				return nil, fmt.Errorf("reference image must be opaque; remove transparent pixels")
			}
			pixels[y*w+x] = rgbPixel{c.R, c.G, c.B}
		}
	}
	return pixels, nil
}
func difference(a, b uint8) int {
	n := int(a) - int(b)
	if n < 0 {
		return -n
	}
	return n
}

// MatchImage finds same-scale reference regions without sending input.
// Percentage measures RGB agreement, not a probability of recognizing a control.
func MatchImage(ctx context.Context, img, reference image.Image, opts ImageMatchOptions) (ImageMatchResult, error) {
	result := ImageMatchResult{CoordinateSpace: "image_pixels", Matches: make([]ImageMatch, 0)}
	if math.IsNaN(opts.MinimumMatch) || math.IsInf(opts.MinimumMatch, 0) || opts.MinimumMatch <= 0 || opts.MinimumMatch > 100 {
		return result, fmt.Errorf("minimum match must be greater than zero and at most 100 percent")
	}
	limit := opts.MaxResults
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("maximum results must be between 1 and 100")
	}
	pixels, err := rgbImage(ctx, img, maxImagePixels, false)
	if err != nil {
		return result, err
	}
	sample, err := rgbImage(ctx, reference, maxReferencePixels, true)
	if err != nil {
		return result, err
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	rw, rh := reference.Bounds().Dx(), reference.Bounds().Dy()
	result.Width, result.Height = w, h
	if rw > w || rh > h {
		return result, nil
	}
	// High-contrast reference pixels reject unrelated flat backgrounds earlier.
	var sumR, sumG, sumB int64
	for _, c := range sample {
		sumR += int64(c.r)
		sumG += int64(c.g)
		sumB += int64(c.b)
	}
	avg := rgbPixel{uint8(sumR / int64(len(sample))), uint8(sumG / int64(len(sample))), uint8(sumB / int64(len(sample)))}
	points := make([]referencePixel, len(sample))
	for i, c := range sample {
		points[i] = referencePixel{i % rw, i / rw, c, difference(c.r, avg.r) + difference(c.g, avg.g) + difference(c.b, avg.b)}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].contrast > points[j].contrast })
	denominator := float64(len(sample)) * 3 * 255
	allowance := (1 - opts.MinimumMatch/100) * denominator
	comparisons := 0
	for y := 0; y <= h-rh; y++ {
		if err := ctx.Err(); err != nil {
			return ImageMatchResult{}, err
		}
		for x := 0; x <= w-rw; x++ {
			total := 0
			matched := true
			for _, p := range points {
				comparisons++
				if comparisons > maxMatchComparisons {
					return ImageMatchResult{}, fmt.Errorf("image search exceeds %d pixel comparisons; use a smaller search region or reference", maxMatchComparisons)
				}
				if comparisons%4096 == 0 {
					if err := ctx.Err(); err != nil {
						return ImageMatchResult{}, err
					}
				}
				c := pixels[(y+p.y)*w+x+p.x]
				total += difference(c.r, p.rgb.r) + difference(c.g, p.rgb.g) + difference(c.b, p.rgb.b)
				if float64(total) > allowance+1e-9 {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			match := ImageMatch{X: x, Y: y, Width: rw, Height: rh, MatchPercent: 100 * (1 - float64(total)/denominator)}
			rect := image.Rect(x, y, x+rw, y+rh)
			merged := false
			for i, m := range result.Matches {
				other := image.Rect(m.X, m.Y, m.X+m.Width, m.Y+m.Height)
				overlap := rect.Intersect(other)
				intersection := overlap.Dx() * overlap.Dy()
				union := 2*rw*rh - intersection
				if !overlap.Empty() && float64(intersection)/float64(union) > 0.3 {
					if match.MatchPercent > m.MatchPercent {
						result.Matches[i] = match
					}
					merged = true
					break
				}
			}
			if !merged {
				if len(result.Matches) == limit {
					result.Truncated = true
					result.Matched = true
					return finishMatches(result), nil
				}
				result.Matches = append(result.Matches, match)
			}
		}
	}
	result.Matched = len(result.Matches) > 0
	return finishMatches(result), nil
}
func finishMatches(r ImageMatchResult) ImageMatchResult {
	sort.SliceStable(r.Matches, func(i, j int) bool { return r.Matches[i].MatchPercent > r.Matches[j].MatchPercent })
	return r
}
