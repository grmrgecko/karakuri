package connops

import (
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
)

// HADriver resolves a connection ID to its running Home Assistant driver.
func HADriver(id string) (*homeassistant.Driver, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != homeassistant.Kind {
		return nil, apperrors.ErrValidation.WithMessage("connection is not a Home Assistant connection")
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	driver, ok := d.(*homeassistant.Driver)
	if !ok {
		return nil, apperrors.ErrInternal.WithMessage("connection driver is not a Home Assistant driver")
	}
	return driver, nil
}

// AIDriver resolves a connection ID to its running AI provider driver.
func AIDriver(id string) (*ai.Driver, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != ai.KindAI {
		return nil, apperrors.ErrValidation.WithMessage("connection is not an AI provider connection")
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	driver, ok := d.(*ai.Driver)
	if !ok {
		return nil, apperrors.ErrInternal.WithMessage("connection driver is not an AI provider driver")
	}
	return driver, nil
}

// MQTTDriver resolves a connection ID to its running MQTT driver.
func MQTTDriver(id string) (*mqtt.Driver, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != mqtt.Kind {
		return nil, apperrors.ErrValidation.WithMessage("connection is not an MQTT connection")
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	driver, ok := d.(*mqtt.Driver)
	if !ok {
		return nil, apperrors.ErrInternal.WithMessage("connection driver is not an MQTT driver")
	}
	return driver, nil
}

// ESPHomeDriver resolves a connection ID to its running ESPHome driver.
func ESPHomeDriver(id string) (*esphome.Driver, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != esphome.Kind {
		return nil, apperrors.ErrValidation.WithMessage("connection is not an ESPHome connection")
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	driver, ok := d.(*esphome.Driver)
	if !ok {
		return nil, apperrors.ErrInternal.WithMessage("connection driver is not an ESPHome driver")
	}
	return driver, nil
}

// OpenHABDriver resolves a connection ID to its running openHAB driver.
func OpenHABDriver(id string) (*openhab.Driver, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != openhab.Kind {
		return nil, apperrors.ErrValidation.WithMessage("connection is not an openHAB connection")
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	driver, ok := d.(*openhab.Driver)
	if !ok {
		return nil, apperrors.ErrInternal.WithMessage("connection driver is not an openHAB driver")
	}
	return driver, nil
}

// NVRDriver resolves a connection ID to its running network video recorder.
func NVRDriver(id string) (nvr.NVR, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		if c.Kind != frigate.Kind {
			return nil, apperrors.ErrValidation.WithMessage("connection is not an NVR connection")
		}
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	recorder, ok := d.(nvr.NVR)
	if !ok {
		return nil, apperrors.ErrValidation.WithMessage("connection is not an NVR connection")
	}
	return recorder, nil
}

// FrigateDriver resolves a connection ID to its running Frigate driver.
func FrigateDriver(id string) (*frigate.Driver, error) {
	recorder, err := NVRDriver(id)
	if err != nil {
		return nil, err
	}
	driver, ok := recorder.(*frigate.Driver)
	if !ok {
		return nil, apperrors.ErrValidation.WithMessage("connection is not a Frigate connection")
	}
	return driver, nil
}
