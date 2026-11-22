package homeassistant

import (
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindDef returns the Home Assistant connection kind: the driver factory plus
// the config schema the settings UI renders and the validation and test hooks
// the API calls. Registered by the Home Assistant plugin.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "Home Assistant",
		ConfigSpec: append([]flow.FieldSpec{
			{
				ID: "base_url", Label: "Base URL", Type: flow.FieldString,
				Required: true, Placeholder: "http://homeassistant.local:8123",
				Help: "Root URL of the Home Assistant instance.",
			},
			{
				ID: "token", Label: "Access token", Type: flow.FieldString,
				Required: true, Control: "password",
				Help: "Long-lived access token from the Home Assistant profile page.",
			},
		}, integrations.AutoImportFields(
			flow.FieldSpec{ID: integrations.ImportAreasKey, Label: "Only areas", Type: flow.FieldString,
				Placeholder: "kitchen, living_room",
				Help:        "Comma-separated area IDs, area names, or floors. Empty imports every area, and entities in no area."},
			flow.FieldSpec{ID: integrations.ImportDomainsKey, Label: "Only domains", Type: flow.FieldString,
				Placeholder: "light, switch, sensor",
				Help:        "Comma-separated entity domains. Empty imports every domain."},
		)...),
		Secrets: []string{"token"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid homeassistant config: %w", err)
			}
			return c.Validate()
		},
		Test: TestConnection,
	}
}
