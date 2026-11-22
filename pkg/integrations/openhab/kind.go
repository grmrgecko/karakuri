package openhab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindDef returns the openHAB connection kind.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "openHAB",
		ConfigSpec: append([]flow.FieldSpec{
			{
				ID: "base_url", Label: "Base URL", Type: flow.FieldString, Required: true,
				Placeholder: "http://openhab.local:8080",
				Help:        "Address of the openHAB server, without /rest.",
			},
			{
				ID: "token", Label: "API token", Type: flow.FieldString, Control: "password",
				Help: "Token from the openHAB profile page (API tokens). Empty connects anonymously, " +
					"which openHAB allows by default; a token is needed when anonymous access is turned off.",
			},
		}, integrations.AutoImportFields(
			flow.FieldSpec{ID: integrations.ImportLocationsKey, Label: "Only locations", Type: flow.FieldString,
				Placeholder: "Kitchen, gLivingRoom",
				Help:        "Comma-separated location item names, location labels, or floors. Empty imports every location, and items in none."},
			flow.FieldSpec{ID: integrations.ImportTypesKey, Label: "Only item types", Type: flow.FieldString,
				Placeholder: "Switch, Dimmer, Number",
				Help:        "Comma-separated item types. Number matches dimensioned numbers too. Empty imports every type."},
		)...),
		Secrets: []string{"token"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid openhab config: %w", err)
			}
			return c.Validate()
		},
		Probe: Probe,
	}
}

// Probe reads the server's version and item count with a throwaway client.
func Probe(ctx context.Context, _ uuid.UUID, raw []byte) (string, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", fmt.Errorf("invalid openhab config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	client := &http.Client{}
	version, err := readVersion(ctx, client, cfg)
	if err != nil {
		return "", err
	}
	items, err := readItems(ctx, client, cfg)
	if err != nil {
		return "", err
	}
	auth := "anonymous"
	if cfg.Token != "" {
		auth = "authenticated"
	}
	return fmt.Sprintf("openHAB %s, %d items, %s", version, len(items), auth), nil
}
