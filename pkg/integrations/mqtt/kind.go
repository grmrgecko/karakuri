package mqtt

import (
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindDef returns the MQTT connection kind: the driver factory plus the
// config schema the settings UI renders and the validation and test hooks the
// API calls. Registered by the MQTT plugin.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "MQTT",
		ConfigSpec: append([]flow.FieldSpec{
			{
				ID: "broker_url", Label: "Broker URL", Type: flow.FieldString,
				Required: true, Placeholder: "mqtt://10.0.0.5:1883",
				Help: "mqtt://, mqtts://, ws://, or wss:// URL of the broker.",
			},
			{
				ID: "client_id", Label: "Client ID", Type: flow.FieldString,
				Placeholder: "karakuri",
				Help:        "MQTT client identifier. Left empty, one is derived from the connection ID.",
			},
			{ID: "username", Label: "Username", Type: flow.FieldString},
			{
				ID: "password", Label: "Password", Type: flow.FieldString,
				Control: "password",
			},
			{
				ID: "keep_alive_s", Label: "Keep-alive (seconds)", Type: flow.FieldNumber,
				Placeholder: "30",
			},
			{
				ID: "clean_start", Label: "Clean start", Type: flow.FieldBoolean,
				Help: "Start each session fresh instead of resuming subscriptions.",
			},
		}, integrations.AutoImportFields(
			flow.FieldSpec{ID: integrations.DiscoveryPrefixKey, Label: "Discovery prefix", Type: flow.FieldString,
				Default: integrations.DefaultDiscoveryPrefix, Placeholder: integrations.DefaultDiscoveryPrefix,
				Help: "Topic prefix devices publish Home Assistant MQTT discovery under."},
			flow.FieldSpec{ID: integrations.Z2MBaseKey, Label: "Zigbee2MQTT base topic", Type: flow.FieldString,
				Default: integrations.DefaultZ2MBase, Placeholder: integrations.DefaultZ2MBase,
				Help: "Base topic of the Zigbee2MQTT instance to import from. Set it to off to turn Zigbee2MQTT import off."},
		)...),
		Secrets: []string{"password"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid mqtt config: %w", err)
			}
			if c.BrokerURL == "" {
				return fmt.Errorf("mqtt config requires broker_url")
			}
			return nil
		},
		Test: TestConnection,
	}
}
