package core

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	xdraw "golang.org/x/image/draw"
)

// ImageEditNode resizes, crops, rotates, and re-encodes an image.
//
// The reason it earns a place in a home-automation engine is cost rather than
// looks: an agent screenshot at native resolution is an expensive image part,
// and a model that only needs to read a thermostat does not need four megapixels
// of it. Halving each side quarters the pixels, and the token count roughly with
// them.
//
// It reads and writes bytes rather than the image value type, so it sits between
// screenshot, file-read, http-request, and file-write without a wrapper on
// either side. What it does declare is meta.mime on the way out, which is what
// lets the AI agent attach the result: bytes whose type nobody declared are
// refused there rather than guessed at.
type ImageEditNode struct{}

func (ImageEditNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "image-edit",
		Label:       "Image Edit",
		Folder:      flow.FolderUtility,
		Description: "Resizes, crops, rotates, and re-encodes an image",
		Icon:        "las la-crop",
		Inputs:      converterInput(flow.PortBinary),
		Outputs:     converterOutput(flow.PortBinary),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "fit",
				Label:   "Resize",
				Type:    flow.FieldSelect,
				Default: "inside",
				Options: []engine.Option{
					{Value: "inside", Label: "Fit inside the box, keeping the shape"},
					{Value: "cover", Label: "Fill the box and crop the overflow"},
					{Value: "exact", Label: "Stretch to exactly the box"},
					{Value: "none", Label: "Leave the size alone"},
				},
				Help: "Fitting inside never enlarges a smaller image, so a box is a ceiling rather than a target.",
			},
			{
				ID:          "width",
				Label:       "Width",
				Type:        flow.FieldString,
				Placeholder: "1024",
				ShowIf:      &flow.ShowIf{Field: "fit", Equals: []string{"inside", "cover", "exact"}},
				Help:        "In pixels. Empty derives it from the height and the image's shape.",
			},
			{
				ID:          "height",
				Label:       "Height",
				Type:        flow.FieldString,
				Placeholder: "1024",
				ShowIf:      &flow.ShowIf{Field: "fit", Equals: []string{"inside", "cover", "exact"}},
				Help:        "In pixels. Empty derives it from the width and the image's shape.",
			},
			{
				ID:          "crop",
				Label:       "Crop",
				Type:        flow.FieldString,
				Placeholder: "0,0,640,480",
				Help: "x,y,width,height in pixels, taken before any resize. Empty crops nothing. " +
					"A rectangle reaching past the edge is clipped to it.",
			},
			{
				ID:      "rotate",
				Label:   "Rotate",
				Type:    flow.FieldSelect,
				Default: "0",
				Options: []engine.Option{
					{Value: "0", Label: "None"},
					{Value: "90", Label: "90° clockwise"},
					{Value: "180", Label: "180°"},
					{Value: "270", Label: "270° clockwise"},
				},
				Help: "Quarter turns only. Anything else would have to invent the corner pixels.",
			},
			{
				ID:      "flip",
				Label:   "Flip",
				Type:    flow.FieldSelect,
				Default: "none",
				Options: []engine.Option{
					{Value: "none", Label: "None"},
					{Value: "horizontal", Label: "Horizontally"},
					{Value: "vertical", Label: "Vertically"},
				},
			},
			{
				ID:      "format",
				Label:   "Write as",
				Type:    flow.FieldSelect,
				Default: "jpeg",
				Options: []engine.Option{
					{Value: "jpeg", Label: "JPEG"},
					{Value: "png", Label: "PNG"},
					{Value: "gif", Label: "GIF"},
				},
				Help: "JPEG is the one to send to a model: a screenshot as PNG is several times the bytes for the same picture.",
			},
			{
				ID:          "quality",
				Label:       "JPEG quality",
				Type:        flow.FieldString,
				Default:     "80",
				Placeholder: "80",
				ShowIf:      &flow.ShowIf{Field: "format", Equals: "jpeg"},
				Help:        "1 to 100. Above about 90 the file grows much faster than the picture improves.",
			},
		},
	}
}

// imageEditConfig is the decoded image-edit node configuration.
type imageEditConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	Fit      string `json:"fit"`
	Width    string `json:"width"`
	Height   string `json:"height"`
	Crop     string `json:"crop"`
	Rotate   string `json:"rotate"`
	Flip     string `json:"flip"`
	Format   string `json:"format"`
	Quality  string `json:"quality"`
}

// Execute decodes the image, applies the configured edits in a fixed order, and
// re-encodes it.
func (ImageEditNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg imageEditConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	data, err := archiveBytes(value, property)
	if err != nil {
		return nil, err
	}

	// Decode. image.Decode picks the format from the registered decoders, which
	// is reading a container header rather than guessing what a payload means,
	// so the no-sniffing rule is not in play: the bytes either are a PNG or
	// they are not.
	// The header alone sizes the pixel buffer, so a few hundred bytes claiming
	// a 40000x40000 canvas would allocate gigabytes before a pixel is checked.
	const maxImagePixels = 64 << 20
	header, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the image: %w", err)
	}
	if header.Width <= 0 || header.Height <= 0 || header.Width > maxImagePixels/header.Height {
		return nil, fmt.Errorf("the image is %dx%d, over the %d pixel limit", header.Width, header.Height, maxImagePixels)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the image: %w", err)
	}

	// Edit, in a fixed order so the settings compose predictably: crop picks
	// the region, resize sizes it, then the image is turned.
	img, err := cropImage(src, cfg.Crop)
	if err != nil {
		return nil, err
	}
	img, err = resizeImage(img, cfg)
	if err != nil {
		return nil, err
	}
	img, err = rotateImage(img, cfg.Rotate)
	if err != nil {
		return nil, err
	}
	img, err = flipImage(img, cfg.Flip)
	if err != nil {
		return nil, err
	}

	// Encode, and say what came out.
	encoded, mime, err := encodeImage(img, cfg)
	if err != nil {
		return nil, err
	}
	out, err := parserResult(msg, property, cfg.Target, encoded)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out.Meta["mime"] = mime
	out.Meta["width"] = float64(bounds.Dx())
	out.Meta["height"] = float64(bounds.Dy())
	return engine.One("out", out), nil
}

// cropImage takes the configured rectangle, clipped to the image.
func cropImage(src image.Image, spec string) (image.Image, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return src, nil
	}
	parts := strings.Split(spec, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("crop %q is not x,y,width,height", spec)
	}
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("crop %q is not x,y,width,height: %w", spec, err)
		}
		nums[i] = n
	}
	if nums[2] <= 0 || nums[3] <= 0 {
		return nil, fmt.Errorf("crop needs a positive width and height, got %dx%d", nums[2], nums[3])
	}

	b := src.Bounds()
	want := image.Rect(b.Min.X+nums[0], b.Min.Y+nums[1], b.Min.X+nums[0]+nums[2], b.Min.Y+nums[1]+nums[3])
	region := want.Intersect(b)
	if region.Empty() {
		return nil, fmt.Errorf("crop %q lies outside the %dx%d image", spec, b.Dx(), b.Dy())
	}

	// SubImage is the cheap path and every standard decoder's type has it; the
	// draw fallback covers a type that does not.
	type subImager interface {
		SubImage(image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		return si.SubImage(region), nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	xdraw.Draw(dst, dst.Bounds(), src, region.Min, xdraw.Src)
	return dst, nil
}

// resizeImage scales the image according to the fit mode.
func resizeImage(src image.Image, cfg imageEditConfig) (image.Image, error) {
	if cfg.Fit == "none" {
		return src, nil
	}
	width, err := parsePixels(cfg.Width, "width")
	if err != nil {
		return nil, err
	}
	height, err := parsePixels(cfg.Height, "height")
	if err != nil {
		return nil, err
	}
	if width == 0 && height == 0 {
		return src, nil
	}

	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return nil, fmt.Errorf("the image has no size")
	}

	// A missing side is derived from the other one and the image's shape, so
	// "at most 1024 wide" is one field rather than two.
	if width == 0 {
		width = int(float64(height) * float64(sw) / float64(sh))
	}
	if height == 0 {
		height = int(float64(width) * float64(sh) / float64(sw))
	}
	if width < 1 || height < 1 {
		return nil, fmt.Errorf("the requested size works out to %dx%d", width, height)
	}

	switch cfg.Fit {
	case "", "inside":
		// Scale by whichever side is the tighter constraint, and never enlarge:
		// a box is a ceiling, and upscaling to fill it invents detail.
		if width >= sw && height >= sh {
			return src, nil
		}
		// The limiting side is pinned to the box and the other derived from it
		// in integers. A float scale multiplied back lands a hair under the box
		// and truncates a pixel short: 704 * (480/704) is 479.
		if width*sh <= height*sw {
			return scaleTo(src, b, width, max(1, sh*width/sw)), nil
		}
		return scaleTo(src, b, max(1, sw*height/sh), height), nil

	case "exact":
		return scaleTo(src, b, width, height), nil

	case "cover":
		// Scale by the looser constraint so the box is covered, then take the
		// middle of the overflow.
		scale := max(float64(width)/float64(sw), float64(height)/float64(sh))
		scaled := scaleTo(src, b, max(width, int(float64(sw)*scale)), max(height, int(float64(sh)*scale)))
		sb := scaled.Bounds()
		offX := (sb.Dx() - width) / 2
		offY := (sb.Dy() - height) / 2
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.Draw(dst, dst.Bounds(), scaled, sb.Min.Add(image.Pt(offX, offY)), xdraw.Src)
		return dst, nil

	default:
		return nil, fmt.Errorf("unknown resize mode %q", cfg.Fit)
	}
}

// scaleTo resamples the image to the given size. CatmullRom is the slow, sharp
// kernel; a screenshot going to a model is read as text, so softening it to save
// a millisecond is the wrong trade.
func scaleTo(src image.Image, region image.Rectangle, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, region, xdraw.Src, nil)
	return dst
}

// rotateImage turns the image by a quarter turn.
func rotateImage(src image.Image, degrees string) (image.Image, error) {
	switch degrees {
	case "", "0":
		return src, nil
	case "90", "180", "270":
	default:
		return nil, fmt.Errorf("unknown rotation %q", degrees)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if degrees != "180" {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch degrees {
			case "90":
				dst.Set(h-1-y, x, c)
			case "180":
				dst.Set(w-1-x, h-1-y, c)
			case "270":
				dst.Set(y, w-1-x, c)
			}
		}
	}
	return dst, nil
}

// flipImage mirrors the image.
func flipImage(src image.Image, mode string) (image.Image, error) {
	switch mode {
	case "", "none":
		return src, nil
	case "horizontal", "vertical":
	default:
		return nil, fmt.Errorf("unknown flip %q", mode)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			if mode == "horizontal" {
				dst.Set(w-1-x, y, c)
			} else {
				dst.Set(x, h-1-y, c)
			}
		}
	}
	return dst, nil
}

// encodeImage writes the image out and reports the media type it wrote.
func encodeImage(img image.Image, cfg imageEditConfig) ([]byte, string, error) {
	var buf bytes.Buffer
	switch cfg.Format {
	case "", "jpeg":
		quality, err := parsePixels(cfg.Quality, "quality")
		if err != nil {
			return nil, "", err
		}
		if quality == 0 {
			quality = 80
		}
		if quality < 1 || quality > 100 {
			return nil, "", fmt.Errorf("JPEG quality must be 1 to 100, got %d", quality)
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, "", fmt.Errorf("writing JPEG: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil

	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("writing PNG: %w", err)
		}
		return buf.Bytes(), "image/png", nil

	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, "", fmt.Errorf("writing GIF: %w", err)
		}
		return buf.Bytes(), "image/gif", nil

	default:
		return nil, "", fmt.Errorf("unknown image format %q", cfg.Format)
	}
}

// parsePixels reads an optional whole-number field, where empty means unset.
func parsePixels(raw, what string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", what, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s cannot be negative, got %d", what, n)
	}
	return n, nil
}
