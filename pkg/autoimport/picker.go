package autoimport

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/hadiscovery"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// readWindow is how long the picker collects retained discovery configs. The
// broker replays them at once on subscribe, so a quiet window means it is
// done.
const readWindow = 3 * time.Second

// readDiscovery collects the retained discovery configs on a connection.
func readDiscovery(ctx context.Context, connectionID string) (*mqtt.Driver, integrations.AutoImportSettings, []hadiscovery.Config, error) {
	conn, err := connops.Load(connectionID)
	if err != nil {
		return nil, integrations.AutoImportSettings{}, nil, err
	}
	raw, err := conn.ConfigJSON()
	if err != nil {
		return nil, integrations.AutoImportSettings{}, nil, apperrors.ErrInternal.WithMessage(err.Error())
	}
	settings, err := integrations.ReadAutoImport(raw)
	if err != nil {
		return nil, settings, nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	drv, err := connops.MQTTDriver(connectionID)
	if err != nil {
		return nil, settings, nil, err
	}
	if drv.Status().State != integrations.StateConnected {
		return nil, settings, nil, apperrors.ErrBadGateway.WithMessage("the MQTT connection is not connected")
	}

	var mu sync.Mutex
	byKey := map[string]hadiscovery.Config{}
	drop, err := drv.SubscribeMessages(settings.DiscoveryPrefix+"/#", 0, func(msg mqtt.Message) {
		cfgs, err := hadiscovery.Parse(settings.DiscoveryPrefix, msg.Topic, msg.Payload)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, cfg := range cfgs {
			if cfg.Removed {
				for key, c := range byKey {
					if c.Topic == cfg.Topic {
						delete(byKey, key)
					}
				}
				continue
			}
			byKey[cfg.Key] = cfg
		}
	})
	if err != nil {
		return nil, settings, nil, apperrors.ErrBadGateway.WithMessage(err.Error())
	}
	select {
	case <-ctx.Done():
	case <-time.After(readWindow):
	}
	drop()
	mu.Lock()
	defer mu.Unlock()
	out := make([]hadiscovery.Config, 0, len(byKey))
	for _, cfg := range byKey {
		out = append(out, cfg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceName() != out[j].DeviceName() {
			return strings.ToLower(out[i].DeviceName()) < strings.ToLower(out[j].DeviceName())
		}
		return strings.ToLower(out[i].Name()) < strings.ToLower(out[j].Name())
	})
	return drv, settings, out, nil
}

// DiscoverMQTT lists the Home Assistant discovery configs on an MQTT
// connection with the device each would become.
func DiscoverMQTT(ctx context.Context, connectionID string) ([]entitydevices.Discovered, error) {
	connID, err := uuid.Parse(connectionID)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid connection id")
	}
	_, settings, cfgs, err := readDiscovery(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	devices, err := models.ListMQTTDevicesOnConnection(connID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithMessage(err.Error())
	}
	added := map[string]string{}
	for _, d := range devices {
		if d.DiscoveryKey != "" {
			added[d.DiscoveryKey] = d.ID.String()
		}
	}
	out := []entitydevices.Discovered{}
	for _, cfg := range cfgs {
		item := entitydevices.Discovered{ObjectID: cfg.Key, Name: cfg.Name(), Type: cfg.Component, Group: cfg.DeviceName(), CanAdd: true}
		mapped, err := hadiscovery.Map(cfg, connID)
		switch {
		case err != nil:
			item.CanAdd, item.Reason = false, err.Error()
		case settings.Z2MBase != "" && cfg.FromZigbee2MQTT():
			item.CanAdd, item.Reason = false, "a Zigbee2MQTT device; add it from Zigbee2MQTT"
		default:
			item.Kind = mapped.Definition.Control
			item.Commandable = mapped.Definition.PayloadScript != ""
		}
		if id := added[cfg.Key]; id != "" {
			item.DeviceID, item.CanAdd, item.Reason = id, false, "already added"
		}
		out = append(out, item)
	}
	return out, nil
}

// AddMQTT adds the chosen discovery configs as devices.
func AddMQTT(ctx context.Context, connectionID string, keys []string) (entitydevices.AddResult, error) {
	connID, err := uuid.Parse(connectionID)
	if err != nil {
		return entitydevices.AddResult{}, apperrors.ErrValidation.WithMessage("invalid connection id")
	}
	_, _, cfgs, err := readDiscovery(ctx, connectionID)
	if err != nil {
		return entitydevices.AddResult{}, err
	}
	byKey := map[string]hadiscovery.Config{}
	for _, cfg := range cfgs {
		byKey[cfg.Key] = cfg
	}
	result := entitydevices.AddResult{Added: []mqttrelay.View{}, Failed: []entitydevices.AddFailure{}}
	for _, key := range keys {
		cfg, ok := byKey[key]
		if !ok {
			result.Failed = append(result.Failed, entitydevices.AddFailure{ObjectID: key, Error: "no discovery config with that key"})
			continue
		}
		mapped, err := hadiscovery.Map(cfg, connID)
		if err != nil {
			result.Failed = append(result.Failed, entitydevices.AddFailure{ObjectID: key, Error: err.Error()})
			continue
		}
		def := mapped.Definition
		def.Name = uniqueName(def.Name)
		saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, mqttrelay.Origin{DiscoveryKey: key})
		if err != nil {
			result.Failed = append(result.Failed, entitydevices.AddFailure{ObjectID: key, Error: err.Error()})
			continue
		}
		result.Added = append(result.Added, mqttrelay.BuildView(saved, mqttrelay.D))
	}
	return result, nil
}
