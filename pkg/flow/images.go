package flow

import (
	"fmt"
	"regexp"
	"strings"
)

// Images attached to a node are carried in its config as data URIs, keyed by
// name. A note's markdown refers to one as "image:<name>" and the editor swaps
// the data URI in when it renders, so the bytes travel with the graph: an
// exported flow opened on another instance shows the same pictures with
// nothing to fetch and nothing to clean up when a note is deleted.
//
// The rules here are the security boundary for that, not merely a size policy.
// The editor substitutes a stored value straight into an img src, so a value
// that is not plainly an image data URI — a javascript: URL, anything carrying
// a quote that could close the attribute — must not survive being saved.

// MaxImageBytes caps one attached image at 2 MiB of decoded data. Screenshots
// and diagrams fit comfortably; a camera original does not, and a graph
// document is copied into every frozen version, so the cap is what keeps a
// note from quietly turning a flow into a megabyte-scale record.
const MaxImageBytes = 2 << 20

// MaxImagesPerNode bounds how many images one node may carry, so a runaway
// paste loop cannot grow a graph without limit.
const MaxImagesPerNode = 20

// imageNamePattern is the name an image may be stored under. It is also the
// text that appears inside an "image:<name>" reference in markdown, so it is
// restricted to characters that survive a URL unchanged: a name that the
// renderer would escape could never be matched back to its image.
var imageNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// imageDataPattern is the only value shape accepted: a base64 data URI of one
// of the image types every browser renders. The alphabet is exact, which is
// what makes the value safe to place in an HTML attribute.
var imageDataPattern = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/]+={0,2}$`)

// ValidateImages checks an images config value: a map of name to data URI,
// within the per-image and per-node limits. It returns the problems found,
// phrased for the config field they belong to.
func ValidateImages(value any) []string {
	set, ok := value.(map[string]any)
	if !ok {
		return []string{"must be a set of named images"}
	}
	if len(set) > MaxImagesPerNode {
		return []string{fmt.Sprintf("holds %d images; at most %d are allowed", len(set), MaxImagesPerNode)}
	}

	var problems []string
	for name, raw := range set {
		if !imageNamePattern.MatchString(name) {
			problems = append(problems, fmt.Sprintf("image name %q may only use letters, digits, dot, dash, and underscore", name))
			continue
		}
		data, ok := raw.(string)
		if !ok {
			problems = append(problems, fmt.Sprintf("image %q is not a string", name))
			continue
		}
		if !imageDataPattern.MatchString(data) {
			problems = append(problems, fmt.Sprintf("image %q is not a PNG, JPEG, GIF, or WebP data URI", name))
			continue
		}
		if size := decodedSize(data); size > MaxImageBytes {
			problems = append(problems, fmt.Sprintf("image %q is %d bytes; the limit is %d", name, size, MaxImageBytes))
		}
	}
	return problems
}

// decodedSize returns the byte count a base64 data URI decodes to, computed
// from its length rather than by decoding it: the size is all that matters
// here, and a multi-megabyte decode per validation pass is not worth paying.
func decodedSize(data string) int {
	payload := data[strings.IndexByte(data, ',')+1:]
	n := len(payload) / 4 * 3
	if strings.HasSuffix(payload, "==") {
		return n - 2
	}
	if strings.HasSuffix(payload, "=") {
		return n - 1
	}
	return n
}
