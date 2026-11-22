package ainodes

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
)

// imageFromValue decodes a pulled image value — the Image parser's output —
// into the wire's image part.
func imageFromValue(v flow.ImageValue) (ai.Image, error) {
	if v.MIME == "" || v.Data == "" {
		return ai.Image{}, fmt.Errorf("image value is empty; wire an Image node")
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return ai.Image{}, fmt.Errorf("image value holds undecodable data: %w", err)
	}
	return ai.Image{MIME: v.MIME, Data: data}, nil
}

// A binary payload becomes an image for the model only when its producer said
// what it is — the no-sniffing rule. Three declarations are honored, in
// order: a "mime" meta key, a "content_type" meta key (what the webhook and
// http-request nodes stamp), and a "format" meta key naming an image format
// (what the screenshot node stamps). Bytes with no declaration are refused
// with the fix named, never guessed at.

// imageFormats maps a declared format name onto its MIME type.
var imageFormats = map[string]string{
	"png":  "image/png",
	"jpeg": "image/jpeg",
	"jpg":  "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
}

// payloadImage turns a message's binary payload into an image part. ok is
// false when the payload is not bytes; an error means bytes that declare no
// image type.
func payloadImage(msg *engine.Msg) (ai.Image, bool, error) {
	data, isBinary := msg.Payload.([]byte)
	if !isBinary {
		return ai.Image{}, false, nil
	}
	mime, err := declaredImageMIME(msg.Meta)
	if err != nil {
		return ai.Image{}, true, err
	}
	return ai.Image{MIME: mime, Data: data}, true, nil
}

// declaredImageMIME reads the image type a producer declared on the message.
func declaredImageMIME(meta map[string]any) (string, error) {
	if mime, ok := metaString(meta, "mime"); ok {
		if strings.HasPrefix(mime, "image/") {
			return mime, nil
		}
		return "", fmt.Errorf("binary payload declares MIME type %q, which is not an image", mime)
	}
	if ct, ok := metaString(meta, "content_type"); ok && strings.HasPrefix(ct, "image/") {
		// Content types can carry parameters ("image/png; something"); the
		// providers want the bare type.
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
		return ct, nil
	}
	if format, ok := metaString(meta, "format"); ok {
		if mime, known := imageFormats[strings.ToLower(format)]; known {
			return mime, nil
		}
	}
	return "", fmt.Errorf("binary payload declares no image type; set meta.mime (image/png), or produce it from a node that declares one, like screenshot")
}

// metaString reads one meta key as a non-empty string.
func metaString(meta map[string]any, key string) (string, bool) {
	if meta == nil {
		return "", false
	}
	s, ok := meta[key].(string)
	s = strings.TrimSpace(strings.ToLower(s))
	return s, ok && s != ""
}
