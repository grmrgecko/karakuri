package esphome

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindDef returns the ESPHome connection kind.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "ESPHome",
		ConfigSpec: append([]flow.FieldSpec{
			{
				ID: "host", Label: "Host", Type: flow.FieldString, Required: true,
				Control: "esphome-host", Placeholder: "garage.local",
				Help: "Address or name of the node. Find nodes lists the ones announcing themselves on this network.",
			},
			{
				ID: "port", Label: "Port", Type: flow.FieldNumber, Placeholder: "6053",
				Help: "Native API port, 6053 unless the node's api: block sets another.",
			},
			{
				ID: "encryption_key", Label: "Encryption key", Type: flow.FieldString, Control: "password",
				Help: "The base64 key from the node's api: encryption: key: setting.",
			},
			{
				ID: "password", Label: "Password", Type: flow.FieldString, Control: "password",
				Help: "Legacy API password, for older nodes without an encryption key.",
			},
		}, integrations.AutoImportFields()...),
		Secrets: []string{"encryption_key", "password"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := decodeConfig(cfg, &c); err != nil {
				return err
			}
			return c.validate()
		},
		Probe: Probe,
	}
}

// decodeConfig reads a stored config.
func decodeConfig(raw []byte, cfg *Config) error {
	if err := json.Unmarshal(raw, cfg); err != nil {
		return fmt.Errorf("invalid esphome config: %w", err)
	}
	return nil
}

// Probe connects once, reads the node's information and entity count, and
// disconnects.
func Probe(ctx context.Context, _ uuid.UUID, raw []byte) (string, error) {
	var cfg Config
	if err := decodeConfig(raw, &cfg); err != nil {
		return "", err
	}
	if err := cfg.validate(); err != nil {
		return "", err
	}
	d := &Driver{entities: map[string]Entity{}, byKey: map[uint32]string{}, actions: map[string]Action{},
		values: map[string]map[string]any{}}
	c, info, err := dial(cfg.address(), cfg, d.onMessage)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	entities, _, err := d.list(c)
	if err != nil {
		return "", err
	}
	detail := info.Name
	if info.Board != "" {
		detail += " (" + info.Board + ")"
	}
	if info.Version != "" {
		detail += ", ESPHome " + info.Version
	}
	if info.MAC != "" {
		detail += ", MAC " + info.MAC
	}
	return fmt.Sprintf("%s, %d entities", detail, len(entities)), nil
}

// splitHostPort splits an address into host and numeric port.
func splitHostPort(addr string) (string, int, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(port)
	return host, n, err
}
