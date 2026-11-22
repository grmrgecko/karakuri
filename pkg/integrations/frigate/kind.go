package frigate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindDef returns the Frigate connection kind.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "Frigate NVR",
		ConfigSpec: append([]flow.FieldSpec{
			{
				ID: "url", Label: "URL", Type: flow.FieldString, Required: true,
				Placeholder: "http://frigate.local:5000",
				Help:        "Frigate's address. The internal port 5000 needs no login; the authenticated port 8971 needs a username and password.",
			},
			{ID: "username", Label: "Username", Type: flow.FieldString, Help: "Frigate user, when the URL needs a login."},
			{ID: "password", Label: "Password", Type: flow.FieldString, Control: "password"},
			{ID: "tls_skip_verify", Label: "Skip TLS verification", Type: flow.FieldBoolean,
				Help: "Accept any certificate, such as Frigate's self-signed one on port 8971."},
			{ID: "ca_cert", Label: "CA certificate", Type: flow.FieldText,
				Help: "PEM certificate to trust instead of the system's, for a server with its own CA."},
		}, integrations.AutoImportFields()...),
		Secrets: []string{"password"},
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
		return fmt.Errorf("invalid frigate config: %w", err)
	}
	return nil
}

// Probe logs in, reads the version and cameras, and reports them.
func Probe(ctx context.Context, _ uuid.UUID, raw []byte) (string, error) {
	var cfg Config
	if err := decodeConfig(raw, &cfg); err != nil {
		return "", err
	}
	if err := cfg.validate(); err != nil {
		return "", err
	}
	c := newClient(cfg)
	if err := c.login(ctx); err != nil {
		return "", err
	}
	version, err := c.version(ctx)
	if err != nil {
		return "", err
	}
	var fc frigateConfig
	if err := c.getJSON(ctx, "/api/config", maxConfigBytes, &fc); err != nil {
		return "", err
	}
	names := make([]string, 0, len(fc.Cameras))
	for name := range fc.Cameras {
		names = append(names, name)
	}
	sort.Strings(names)
	detail := fmt.Sprintf("Frigate %s, %d cameras", version, len(names))
	if len(names) == 1 {
		detail = fmt.Sprintf("Frigate %s, 1 camera", version)
	}
	if len(names) > 0 {
		detail += ": " + strings.Join(names, ", ")
	}
	return detail, nil
}
