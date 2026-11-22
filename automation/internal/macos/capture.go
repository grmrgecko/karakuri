//go:build darwin

package macos

import (
	"fmt"
	"image"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// The screen is read out of CoreGraphics: CGDisplayCreateImage photographs a
// display into a CGImage whose pixels are copied straight into a Go image,
// replacing the screencapture spawn and its temp file. The same
// screen-recording permission gates both. The permission is checked and
// requested before this path runs because an unapproved capture still returns
// an image with every application window removed. A pixel format this code
// does not recognise falls back to screencapture rather than guessing at a
// channel order, because a screenshot with red and blue swapped answers no
// error and looks almost right.

var (
	cgGetActiveDisplayList         func(uint32, *uint32, *uint32) int32
	cgPreflightScreenCaptureAccess func() bool
	cgRequestScreenCaptureAccess   func() bool
	cgDisplayCreateImage           func(uint32) uintptr
	cgImageGetWidth                func(uintptr) uint
	cgImageGetHeight               func(uintptr) uint
	cgImageGetBytesPerRow          func(uintptr) uint
	cgImageGetBitsPerPixel         func(uintptr) uint
	cgImageGetBitmapInfo           func(uintptr) uint32
	cgImageGetDataProvider         func(uintptr) uintptr
	cgDataProviderCopyData         func(uintptr) uintptr
	cfDataGetLength                func(uintptr) int
	cfDataGetBytePtr               func(uintptr) *byte
)

var (
	captureOnce sync.Once
	captureErr  error
)

// loadCapture binds the display and image functions once, on the same
// frameworks the main loader opens.
func loadCapture() error {
	if err := load(); err != nil {
		return err
	}
	captureOnce.Do(func() {
		const flags = purego.RTLD_NOW | purego.RTLD_GLOBAL
		app, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", flags)
		if err != nil {
			captureErr = fmt.Errorf("opening ApplicationServices: %w", err)
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", flags)
		if err != nil {
			captureErr = fmt.Errorf("opening CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgGetActiveDisplayList, app, "CGGetActiveDisplayList")
		purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, app, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgRequestScreenCaptureAccess, app, "CGRequestScreenCaptureAccess")
		purego.RegisterLibFunc(&cgDisplayCreateImage, app, "CGDisplayCreateImage")
		purego.RegisterLibFunc(&cgDisplayBounds, app, "CGDisplayBounds")
		purego.RegisterLibFunc(&cgImageGetWidth, app, "CGImageGetWidth")
		purego.RegisterLibFunc(&cgImageGetHeight, app, "CGImageGetHeight")
		purego.RegisterLibFunc(&cgImageGetBytesPerRow, app, "CGImageGetBytesPerRow")
		purego.RegisterLibFunc(&cgImageGetBitsPerPixel, app, "CGImageGetBitsPerPixel")
		purego.RegisterLibFunc(&cgImageGetBitmapInfo, app, "CGImageGetBitmapInfo")
		purego.RegisterLibFunc(&cgImageGetDataProvider, app, "CGImageGetDataProvider")
		purego.RegisterLibFunc(&cgDataProviderCopyData, app, "CGDataProviderCopyData")
		purego.RegisterLibFunc(&cfDataGetLength, cf, "CFDataGetLength")
		purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
	})
	return captureErr
}

// CGBitmapInfo masks and the values a display capture uses.
const (
	cgBitmapAlphaInfoMask = 0x1F
	cgBitmapByteOrderMask = 0x7000

	cgImageAlphaPremultipliedLast  = 1
	cgImageAlphaPremultipliedFirst = 2
	cgImageAlphaNoneSkipLast       = 5
	cgImageAlphaNoneSkipFirst      = 6

	cgBitmapByteOrderDefault  = 0x0000
	cgBitmapByteOrder32Big    = 0x4000
	cgBitmapByteOrder32Little = 0x2000
)

// nativeCaptureScreen answers a capture request from CoreGraphics. A region is
// in the chosen display's own point coordinates, scaled onto the capture's
// pixels, so a Retina screen crops the pixels the points name.
func nativeCaptureScreen(req autoapi.CaptureRequest) (image.Image, error) {
	displays, err := activeDisplays()
	if err != nil {
		return nil, err
	}
	if req.Display < 0 || req.Display >= len(displays) {
		return nil, fmt.Errorf("screen %d does not exist; this session has %d", req.Display, len(displays))
	}
	img, scale, err := nativeCapture(displays[req.Display])
	if err != nil {
		return nil, err
	}
	bounds := cgDisplayBounds(displays[req.Display])
	if bounds.Size.X <= 0 || bounds.Size.Y <= 0 {
		return img, nil
	}
	scaleY := float64(img.Bounds().Dy()) / bounds.Size.Y
	mapping := autoapi.CaptureMapping{OriginX: bounds.Origin.X, OriginY: bounds.Origin.Y, UnitsPerPixelX: 1 / scale, UnitsPerPixelY: 1 / scaleY}
	if !req.Region {
		return autoapi.WithCaptureMapping(img, &mapping), nil
	}
	region := image.Rect(0, 0, req.Width, req.Height).
		Add(image.Pt(req.X, req.Y))
	region = image.Rect(
		int(float64(region.Min.X)*scale), int(float64(region.Min.Y)*scaleY),
		int(float64(region.Max.X)*scale), int(float64(region.Max.Y)*scaleY))
	clipped := region.Intersect(img.Bounds())
	if clipped.Empty() {
		return nil, fmt.Errorf("the region %dx%d at %d,%d is outside screen %d",
			req.Width, req.Height, req.X, req.Y, req.Display)
	}
	mapping.OriginX += float64(clipped.Min.X) / scale
	mapping.OriginY += float64(clipped.Min.Y) / scaleY
	return autoapi.WithCaptureMapping(img.SubImage(clipped), &mapping), nil
}

// requireScreenCaptureAccess asks macOS for screen-recording access before a
// capture. Without it, CoreGraphics returns the wallpaper and menu bar while
// silently omitting application windows.
func requireScreenCaptureAccess() error {
	if err := loadCapture(); err != nil {
		return err
	}
	if cgPreflightScreenCaptureAccess() || cgRequestScreenCaptureAccess() {
		return nil
	}
	return fmt.Errorf("screen recording access is required; allow the responsible application in System Settings > Privacy & Security > Screen Recording (sshd-session and sshd-keygen-wrapper for SSH), then try again")
}

// activeDisplays lists the displays in arrangement order, main display first.
func activeDisplays() ([]uint32, error) {
	if err := loadCapture(); err != nil {
		return nil, err
	}
	ids := make([]uint32, 16)
	var count uint32
	if status := cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &count); status != 0 {
		return nil, fmt.Errorf("listing displays failed (status %d)", status)
	}
	if count == 0 {
		return nil, fmt.Errorf("this session reports no displays")
	}
	return ids[:count], nil
}

// nativeCapture photographs one display and returns it as a Go image, along
// with the capture's pixels-per-point scale so a caller cropping by point
// coordinates can convert.
func nativeCapture(display uint32) (*image.RGBA, float64, error) {
	cgImage := cgDisplayCreateImage(display)
	if cgImage == 0 {
		return nil, 0, fmt.Errorf("CoreGraphics returned no image, which is what a session " +
			"without the screen-recording permission gets")
	}
	defer cfRelease(cgImage)

	width := int(cgImageGetWidth(cgImage))
	height := int(cgImageGetHeight(cgImage))
	stride := int(cgImageGetBytesPerRow(cgImage))
	if cgImageGetBitsPerPixel(cgImage) != 32 {
		return nil, 0, fmt.Errorf("the capture is %d bits per pixel, not the 32 this reader handles",
			cgImageGetBitsPerPixel(cgImage))
	}

	// The channel order is fully determined by the bitmap info: where the
	// alpha byte sits and which end of the word comes first in memory.
	info := cgImageGetBitmapInfo(cgImage)
	alpha := info & cgBitmapAlphaInfoMask
	order := info & cgBitmapByteOrderMask
	alphaFirst := alpha == cgImageAlphaPremultipliedFirst || alpha == cgImageAlphaNoneSkipFirst
	alphaLast := alpha == cgImageAlphaPremultipliedLast || alpha == cgImageAlphaNoneSkipLast

	var r, g, b int
	switch {
	case order == cgBitmapByteOrder32Little && alphaFirst:
		r, g, b = 2, 1, 0 // BGRA in memory.
	case (order == cgBitmapByteOrder32Big || order == cgBitmapByteOrderDefault) && alphaFirst:
		r, g, b = 1, 2, 3 // ARGB in memory.
	case (order == cgBitmapByteOrder32Big || order == cgBitmapByteOrderDefault) && alphaLast:
		r, g, b = 0, 1, 2 // RGBA in memory.
	case order == cgBitmapByteOrder32Little && alphaLast:
		r, g, b = 3, 2, 1 // ABGR in memory.
	default:
		return nil, 0, fmt.Errorf("the capture's pixel format (bitmap info %#x) is not one this reader handles", info)
	}

	data := cgDataProviderCopyData(cgImageGetDataProvider(cgImage))
	if data == 0 {
		return nil, 0, fmt.Errorf("the capture's pixels could not be copied")
	}
	defer cfRelease(data)
	length := cfDataGetLength(data)
	if length < stride*height {
		return nil, 0, fmt.Errorf("the capture holds %d bytes for a %dx%d image", length, width, height)
	}
	bytes := unsafe.Slice(cfDataGetBytePtr(data), length)

	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := bytes[y*stride:]
		outRow := out.Pix[y*out.Stride:]
		for x := 0; x < width; x++ {
			pixel := row[x*4 : x*4+4]
			outRow[x*4+0] = pixel[r]
			outRow[x*4+1] = pixel[g]
			outRow[x*4+2] = pixel[b]
			outRow[x*4+3] = 0xFF
		}
	}

	scale := float64(width) / cgDisplayBounds(display).Size.X
	if scale <= 0 {
		scale = 1
	}
	return out, scale, nil
}
