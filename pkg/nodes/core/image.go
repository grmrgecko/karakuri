package core

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// ImageNode wraps binary bytes as a typed image value. It exists because a
// pulled value is a payload alone — the metadata where a producer declares
// its image type does not survive a value edge — so the author declares the
// type here instead, the way a webhook's body format is declared rather than
// sniffed. The AI agent's Images input consumes these today; any future image
// tool takes the same type.
type ImageNode struct{}

// Meta describes the image node type.
func (ImageNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "image",
		Label:       "Image",
		Folder:      flow.FolderParsers,
		Description: "Wraps binary data as a typed image value for nodes that take images",
		Icon:        "las la-image",
		Pure:        true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Image", Type: flow.PortImage},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "source", Label: "Source", Type: flow.FieldJSON,
				Port: flow.PortBinary, Required: true,
			},
			{
				ID: "mime", Label: "Image type", Type: flow.FieldSelect,
				Required: true, Default: "image/png",
				Options: []flow.Option{
					{Value: "image/png", Label: "PNG"},
					{Value: "image/jpeg", Label: "JPEG"},
					{Value: "image/gif", Label: "GIF"},
					{Value: "image/webp", Label: "WebP"},
				},
				Help: "What the bytes are. A screenshot is PNG; a camera frame is usually JPEG.",
			},
		},
	}
}

// imageNodeConfig is the decoded image node configuration. Source arrives
// over the value edge as the pulled payload's JSON form — base64 for bytes.
type imageNodeConfig struct {
	Source string `json:"source"`
	MIME   string `json:"mime"`
}

// Execute emits the image value.
func (ImageNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg imageNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Source == "" {
		return nil, fmt.Errorf("image node pulled no bytes; wire a binary source")
	}
	if _, err := base64.StdEncoding.DecodeString(cfg.Source); err != nil {
		return nil, fmt.Errorf("image source is not binary data; wire a binary output, like screenshot or a file read as binary")
	}
	if cfg.MIME == "" {
		cfg.MIME = "image/png"
	}
	return engine.One("out", &engine.Msg{
		Payload: flow.ImageValue{MIME: cfg.MIME, Data: cfg.Source},
		Topic:   cfg.MIME,
	}), nil
}
