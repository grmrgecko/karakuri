package automation

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

const maxImageBytes = 32 << 20
const maxImagePixels = 16 << 20

// DecodeImage accepts bounded PNG/JPEG bytes before allocating decoded pixels.
func DecodeImage(data []byte) (image.Image, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, fmt.Errorf("image must contain between 1 and %d bytes", maxImageBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading image dimensions: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("image format must be PNG or JPEG")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImagePixels/cfg.Height {
		return nil, fmt.Errorf("image exceeds %d pixels", maxImagePixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}
	return img, nil
}

// ReadImage reads a bounded host-local reference or captured image.
func ReadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	return DecodeImage(data)
}
