package desktop

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// mediaStatusNode reads the current player without changing it.
type mediaStatusNode struct{}

func (mediaStatusNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type: nodeMediaStatus, Folder: folder, Label: "Media status",
		Description: "Reads playback state and track metadata in the agent user's session",
		Icon:        "las la-music", Pure: true, Retryable: true, TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []nodeapi.FieldSpec{{
			ID: "player", Label: "Player", Type: flow.FieldString, Placeholder: "spotify",
			Help: "MPRIS player name on Linux and FreeBSD, or Music or Spotify on macOS. Empty chooses an active player.",
		}},
	}
}

type mediaStatusConfig struct {
	Player string `json:"player"`
}

func (mediaStatusNode) Execute(ctx context.Context, ec nodeapi.ExecContext, _ map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg mediaStatusConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	status, err := readMediaStatus(ctx, strings.TrimSpace(cfg.Player))
	if err != nil {
		return nil, fmt.Errorf("reading media status: %w", err)
	}
	return nodeapi.One("out", &nodeapi.Msg{Payload: status, Meta: map[string]any{"source": "media-status"}}), nil
}
