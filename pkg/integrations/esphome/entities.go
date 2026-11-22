package esphome

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"google.golang.org/protobuf/proto"
)

// Entity is one component of a node, as its entity listing describes it.
type Entity struct {
	Key      uint32
	ObjectID string
	Name     string
	Type     string
	info     proto.Message
}

// entityFrom reads an entity listing message. Services and the end-of-list
// marker are not entities.
func entityFrom(msg proto.Message) (Entity, bool) {
	e := Entity{info: msg}
	switch m := msg.(type) {
	case *api.ListEntitiesSwitchResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "switch"
	case *api.ListEntitiesBinarySensorResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "binary_sensor"
	case *api.ListEntitiesLightResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "light"
	case *api.ListEntitiesCoverResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "cover"
	case *api.ListEntitiesFanResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "fan"
	case *api.ListEntitiesLockResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "lock"
	case *api.ListEntitiesClimateResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "climate"
	case *api.ListEntitiesNumberResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "number"
	case *api.ListEntitiesSelectResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "select"
	case *api.ListEntitiesTextResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "text"
	case *api.ListEntitiesButtonResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "button"
	case *api.ListEntitiesSensorResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "sensor"
	case *api.ListEntitiesTextSensorResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "text_sensor"
	case *api.ListEntitiesEventResponse:
		e.Key, e.ObjectID, e.Name, e.Type = m.Key, m.ObjectId, m.Name, "event"
	default:
		// Types karakuri lists but cannot draw yet still have a key, an
		// object ID, and a name, read through the generated getters.
		g, ok := msg.(interface {
			GetKey() uint32
			GetObjectId() string
			GetName() string
		})
		if !ok {
			return Entity{}, false
		}
		e.Key, e.ObjectID, e.Name = g.GetKey(), g.GetObjectId(), g.GetName()
		e.Type = strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", msg), "*api.ListEntities"), "Response")
		e.Type = strings.ToLower(e.Type)
	}
	return e, e.ObjectID != ""
}

// Proposal is the device kind an entity becomes.
type Proposal struct {
	Kind     string
	Options  map[string]any
	Writable bool
	// Reason is why the entity cannot be added, empty when it can.
	Reason string
}

// Propose maps an entity to a control kind and its options.
func (e Entity) Propose() Proposal {
	p := Proposal{Options: map[string]any{}}
	switch m := e.info.(type) {
	case *api.ListEntitiesSwitchResponse:
		p.Kind, p.Writable = controls.KindBoolean, true
	case *api.ListEntitiesBinarySensorResponse:
		p.Kind = controls.KindBoolean
		if labels := binaryLabels(m.DeviceClass); labels != nil {
			p.Options["labels"] = labels
		}
	case *api.ListEntitiesLightResponse:
		p.Kind, p.Writable = controls.KindLight, true
		caps := lightCaps(m)
		p.Options["brightness"] = caps&capBrightness != 0
		p.Options["color"] = caps&capRGB != 0
		p.Options["color_temp"] = caps&(capColorTemp|capColdWarm) != 0
		if m.MaxMireds > 0 && m.MinMireds > 0 {
			p.Options["min_kelvin"] = math.Round(1e6 / float64(m.MaxMireds))
			p.Options["max_kelvin"] = math.Round(1e6 / float64(m.MinMireds))
		}
		if effects := lightEffects(m.Effects); len(effects) > 0 {
			p.Options["effects"] = true
			p.Options["effect_list"] = choices(effects)
		}
	case *api.ListEntitiesCoverResponse:
		p.Kind, p.Writable = controls.KindCover, true
		p.Options["position"] = m.SupportsPosition
		p.Options["tilt"] = m.SupportsTilt
		p.Options["stop"] = m.SupportsStop
	case *api.ListEntitiesFanResponse:
		p.Kind, p.Writable = controls.KindFan, true
		p.Options["speed"] = m.SupportsSpeed && m.SupportedSpeedCount > 0
		if m.SupportsSpeed && m.SupportedSpeedCount > 0 {
			p.Options["speed_step"] = math.Round(100/float64(m.SupportedSpeedCount)*100) / 100
		}
		p.Options["presets"] = len(m.SupportedPresetModes) > 0
		if len(m.SupportedPresetModes) > 0 {
			p.Options["preset_list"] = choices(m.SupportedPresetModes)
		}
		p.Options["oscillate"] = m.SupportsOscillation
		p.Options["direction"] = m.SupportsDirection
	case *api.ListEntitiesLockResponse:
		p.Kind, p.Writable = controls.KindLock, true
		if m.RequiresCode {
			p.Writable = false
		}
	case *api.ListEntitiesClimateResponse:
		p.Kind, p.Writable = controls.KindClimate, true
		var modes []string
		for _, mode := range m.SupportedModes {
			modes = append(modes, climateModeName(mode))
		}
		if len(modes) == 0 {
			modes = []string{"off"}
		}
		p.Options["modes"] = choices(modes)
		if m.VisualMinTemperature < m.VisualMaxTemperature {
			p.Options["min_temp"] = widen(m.VisualMinTemperature)
			p.Options["max_temp"] = widen(m.VisualMaxTemperature)
		}
		if m.VisualTargetTemperatureStep > 0 {
			p.Options["step"] = widen(m.VisualTargetTemperatureStep)
		}
		p.Options["unit"] = "°C"
		p.Options["range"] = m.SupportsTwoPointTargetTemperature
		fans := climateFanModes(m)
		p.Options["fan"] = len(fans) > 0
		if len(fans) > 0 {
			p.Options["fan_modes"] = choices(fans)
		}
	case *api.ListEntitiesNumberResponse:
		p.Kind, p.Writable = controls.KindNumber, true
		if m.MinValue < m.MaxValue {
			p.Options["min"], p.Options["max"] = widen(m.MinValue), widen(m.MaxValue)
		}
		if m.Step > 0 {
			p.Options["step"] = widen(m.Step)
		}
		if m.UnitOfMeasurement != "" {
			p.Options["unit"] = m.UnitOfMeasurement
		}
		p.Options["display"] = "box"
		if m.Mode == api.NumberMode_NUMBER_MODE_SLIDER {
			p.Options["display"] = "slider"
		}
	case *api.ListEntitiesSelectResponse:
		p.Kind, p.Writable = controls.KindSelect, true
		if len(m.Options) == 0 {
			p.Reason = "the select lists no options"
			break
		}
		p.Options["options"] = choices(m.Options)
	case *api.ListEntitiesTextResponse:
		p.Kind, p.Writable = controls.KindText, true
		if m.MaxLength > 0 {
			p.Options["max_length"] = float64(m.MaxLength)
		}
	case *api.ListEntitiesButtonResponse:
		p.Kind, p.Writable = controls.KindButtons, true
		p.Options["buttons"] = []any{map[string]any{"value": "press", "label": e.Name}}
	case *api.ListEntitiesMediaPlayerResponse:
		p.Kind, p.Writable = controls.KindMedia, true
		for k, v := range mediaOptions(m) {
			p.Options[k] = v
		}
	case *api.ListEntitiesSensorResponse:
		p.Kind = controls.KindNumber
		p.Options["min"], p.Options["max"] = -1e12, 1e12
		p.Options["display"] = "box"
		if m.UnitOfMeasurement != "" {
			p.Options["unit"] = m.UnitOfMeasurement
		}
		// Zero is a precision too: the node asks for whole numbers.
		if m.AccuracyDecimals >= 0 {
			p.Options["precision"] = float64(m.AccuracyDecimals)
		}
	case *api.ListEntitiesTextSensorResponse:
		p.Kind = controls.KindText
		p.Options["max_length"] = float64(0)
	case *api.ListEntitiesEventResponse:
		p.Kind = controls.KindText
		p.Options["max_length"] = float64(0)
	default:
		p.Reason = e.Type + " entities are not supported yet"
	}
	return p
}

// Values maps a state message to the kind's values. ok is false for a state
// the node reports as missing, which carries no reading.
func (e Entity) Values(state proto.Message) (map[string]any, bool) {
	switch s := state.(type) {
	case *api.SwitchStateResponse:
		return map[string]any{"on": s.State}, true
	case *api.BinarySensorStateResponse:
		if s.MissingState {
			return nil, false
		}
		return map[string]any{"on": s.State}, true
	case *api.LightStateResponse:
		v := map[string]any{"on": s.State}
		info, _ := e.info.(*api.ListEntitiesLightResponse)
		caps := lightCaps(info)
		if caps&capBrightness != 0 {
			v["brightness"] = math.Round(float64(s.Brightness) * 100)
		}
		if caps&capRGB != 0 && uint32(s.ColorMode)&capRGB != 0 {
			v["color"] = rgbHex(s.Red, s.Green, s.Blue)
		}
		if caps&(capColorTemp|capColdWarm) != 0 && s.ColorTemperature > 0 {
			v["color_temp"] = math.Round(1e6 / float64(s.ColorTemperature))
		}
		if s.Effect != "" && !strings.EqualFold(s.Effect, "None") {
			v["effect"] = s.Effect
		}
		return v, true
	case *api.CoverStateResponse:
		v := map[string]any{}
		switch s.CurrentOperation {
		case api.CoverOperation_COVER_OPERATION_IS_OPENING:
			v["state"] = "opening"
		case api.CoverOperation_COVER_OPERATION_IS_CLOSING:
			v["state"] = "closing"
		default:
			if s.Position > 0 {
				v["state"] = "open"
			} else {
				v["state"] = "closed"
			}
		}
		if info, ok := e.info.(*api.ListEntitiesCoverResponse); ok {
			if info.SupportsPosition {
				v["position"] = math.Round(float64(s.Position) * 100)
			}
			if info.SupportsTilt {
				v["tilt"] = math.Round(float64(s.Tilt) * 100)
			}
		}
		return v, true
	case *api.FanStateResponse:
		v := map[string]any{"on": s.State}
		if info, ok := e.info.(*api.ListEntitiesFanResponse); ok {
			if info.SupportsSpeed && info.SupportedSpeedCount > 0 {
				v["percentage"] = math.Round(float64(s.SpeedLevel) * 100 / float64(info.SupportedSpeedCount))
			}
			if info.SupportsOscillation {
				v["oscillating"] = s.Oscillating
			}
			if info.SupportsDirection {
				v["direction"] = "forward"
				if s.Direction == api.FanDirection_FAN_DIRECTION_REVERSE {
					v["direction"] = "reverse"
				}
			}
		}
		if s.PresetMode != "" {
			v["preset"] = s.PresetMode
		}
		return v, true
	case *api.LockStateResponse:
		state := map[api.LockState]string{
			api.LockState_LOCK_STATE_LOCKED:    "locked",
			api.LockState_LOCK_STATE_UNLOCKED:  "unlocked",
			api.LockState_LOCK_STATE_JAMMED:    "jammed",
			api.LockState_LOCK_STATE_LOCKING:   "locking",
			api.LockState_LOCK_STATE_UNLOCKING: "unlocking",
			api.LockState_LOCK_STATE_OPENING:   "unlocking",
			api.LockState_LOCK_STATE_OPEN:      "unlocked",
		}[s.State]
		if state == "" {
			return nil, false
		}
		return map[string]any{"state": state}, true
	case *api.ClimateStateResponse:
		v := map[string]any{"mode": climateModeName(s.Mode)}
		info, _ := e.info.(*api.ListEntitiesClimateResponse)
		// A temperature the node has no reading for arrives as NaN. It is
		// left out, since one NaN makes the whole state unencodable.
		setTemp := func(key string, t float32) {
			if finite(t) {
				v[key] = widen(t)
			}
		}
		if info != nil && info.SupportsTwoPointTargetTemperature {
			setTemp("target_low", s.TargetTemperatureLow)
			setTemp("target_high", s.TargetTemperatureHigh)
		} else {
			setTemp("target", s.TargetTemperature)
		}
		if info == nil || info.SupportsCurrentTemperature {
			setTemp("current", s.CurrentTemperature)
		}
		if info == nil || info.SupportsAction {
			v["action"] = climateActionName(s.Action)
		}
		if s.CustomFanMode != "" {
			v["fan_mode"] = s.CustomFanMode
		} else if info != nil && len(info.SupportedFanModes) > 0 {
			v["fan_mode"] = climateFanName(s.FanMode)
		}
		return v, true
	case *api.NumberStateResponse:
		if s.MissingState || !finite(s.State) {
			return nil, false
		}
		return map[string]any{"value": widen(s.State)}, true
	case *api.SelectStateResponse:
		if s.MissingState {
			return nil, false
		}
		return map[string]any{"value": s.State}, true
	case *api.TextStateResponse:
		if s.MissingState {
			return nil, false
		}
		return map[string]any{"value": s.State}, true
	case *api.SensorStateResponse:
		if s.MissingState || !finite(s.State) {
			return nil, false
		}
		return map[string]any{"value": widen(s.State)}, true
	case *api.TextSensorStateResponse:
		if s.MissingState {
			return nil, false
		}
		return map[string]any{"value": s.State}, true
	case *api.EventResponse:
		return map[string]any{"value": s.EventType}, true
	case *api.MediaPlayerStateResponse:
		state, ok := map[api.MediaPlayerState]string{
			api.MediaPlayerState_MEDIA_PLAYER_STATE_NONE:       "idle",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE:       "idle",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING:    "playing",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED:     "paused",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_ANNOUNCING: "playing",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_OFF:        "off",
			api.MediaPlayerState_MEDIA_PLAYER_STATE_ON:         "on",
		}[s.State]
		if !ok {
			return nil, false
		}
		return map[string]any{
			"state":  state,
			"volume": math.Round(float64(s.Volume) * 100),
			"muted":  s.Muted,
		}, true
	}
	return nil, false
}

// Command builds the request that sets one of the kind's attributes. The
// value has already been coerced by the kind, so a light's brightness is a
// percentage and a colour is a hex string.
func (e Entity) Command(field string, value any) (proto.Message, error) {
	switch e.Type {
	case "switch":
		return &api.SwitchCommandRequest{Key: e.Key, State: truthy(value)}, nil
	case "light":
		req := &api.LightCommandRequest{Key: e.Key}
		switch field {
		case "on", "":
			req.HasState, req.State = true, truthy(value)
		case "brightness":
			pct := number(value)
			req.HasState, req.State = true, pct > 0
			req.HasBrightness, req.Brightness = true, float32(pct/100)
		case "color":
			r, g, b, err := hexRGB(fmt.Sprint(value))
			if err != nil {
				return nil, err
			}
			req.HasState, req.State = true, true
			req.HasRgb, req.Red, req.Green, req.Blue = true, r, g, b
		case "color_temp":
			k := number(value)
			if k <= 0 {
				return nil, fmt.Errorf("colour temperature must be above zero")
			}
			req.HasState, req.State = true, true
			req.HasColorTemperature, req.ColorTemperature = true, float32(1e6/k)
		case "effect":
			req.HasState, req.State = true, true
			req.HasEffect, req.Effect = true, fmt.Sprint(value)
		default:
			return nil, unknownField(e, field)
		}
		return req, nil
	case "cover":
		req := &api.CoverCommandRequest{Key: e.Key}
		switch field {
		case "state", "":
			switch fmt.Sprint(value) {
			case "open":
				req.HasPosition, req.Position = true, 1
			case "closed":
				req.HasPosition, req.Position = true, 0
			case "stopped":
				req.Stop = true
			default:
				return nil, fmt.Errorf("a cover can be told open, closed, or stopped, not %q", value)
			}
		case "position":
			req.HasPosition, req.Position = true, float32(number(value)/100)
		case "tilt":
			req.HasTilt, req.Tilt = true, float32(number(value)/100)
		default:
			return nil, unknownField(e, field)
		}
		return req, nil
	case "fan":
		req := &api.FanCommandRequest{Key: e.Key}
		info, _ := e.info.(*api.ListEntitiesFanResponse)
		switch field {
		case "on", "":
			req.HasState, req.State = true, truthy(value)
		case "percentage":
			count := int32(100)
			if info != nil && info.SupportedSpeedCount > 0 {
				count = info.SupportedSpeedCount
			}
			level := int32(math.Round(number(value) * float64(count) / 100))
			req.HasState, req.State = true, level > 0
			if level > 0 {
				req.HasSpeedLevel, req.SpeedLevel = true, level
			}
		case "preset":
			req.HasPresetMode, req.PresetMode = true, fmt.Sprint(value)
		case "oscillating":
			req.HasOscillating, req.Oscillating = true, truthy(value)
		case "direction":
			req.HasDirection = true
			if fmt.Sprint(value) == "reverse" {
				req.Direction = api.FanDirection_FAN_DIRECTION_REVERSE
			}
		default:
			return nil, unknownField(e, field)
		}
		return req, nil
	case "lock":
		switch fmt.Sprint(value) {
		case "locked":
			return &api.LockCommandRequest{Key: e.Key, Command: api.LockCommand_LOCK_LOCK}, nil
		case "unlocked":
			return &api.LockCommandRequest{Key: e.Key, Command: api.LockCommand_LOCK_UNLOCK}, nil
		}
		return nil, fmt.Errorf("a lock can be told locked or unlocked, not %q", value)
	case "climate":
		req := &api.ClimateCommandRequest{Key: e.Key}
		switch field {
		case "mode", "":
			mode, ok := climateModeValue(fmt.Sprint(value))
			if !ok {
				return nil, fmt.Errorf("unknown climate mode %q", value)
			}
			req.HasMode, req.Mode = true, mode
		case "target":
			req.HasTargetTemperature, req.TargetTemperature = true, float32(number(value))
		case "target_low":
			req.HasTargetTemperatureLow, req.TargetTemperatureLow = true, float32(number(value))
		case "target_high":
			req.HasTargetTemperatureHigh, req.TargetTemperatureHigh = true, float32(number(value))
		case "fan_mode":
			if mode, ok := climateFanValue(fmt.Sprint(value)); ok {
				req.HasFanMode, req.FanMode = true, mode
			} else {
				req.HasCustomFanMode, req.CustomFanMode = true, fmt.Sprint(value)
			}
		default:
			return nil, unknownField(e, field)
		}
		return req, nil
	case "number":
		return &api.NumberCommandRequest{Key: e.Key, State: float32(number(value))}, nil
	case "select":
		return &api.SelectCommandRequest{Key: e.Key, State: fmt.Sprint(value)}, nil
	case "text":
		return &api.TextCommandRequest{Key: e.Key, State: fmt.Sprint(value)}, nil
	case "button":
		return &api.ButtonCommandRequest{Key: e.Key}, nil
	case "mediaplayer":
		req := &api.MediaPlayerCommandRequest{Key: e.Key}
		switch field {
		case "state", "":
			command, ok := map[string]api.MediaPlayerCommand{
				"playing": api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PLAY,
				"paused":  api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PAUSE,
				"idle":    api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_STOP,
				"on":      api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_TURN_ON,
				"off":     api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_TURN_OFF,
			}[fmt.Sprint(value)]
			if !ok {
				return nil, fmt.Errorf("a media player can be told playing, paused, idle, on, or off, not %q", value)
			}
			req.HasCommand, req.Command = true, command
		case "volume":
			req.HasVolume, req.Volume = true, float32(number(value)/100)
		case "muted":
			req.HasCommand, req.Command = true, api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_UNMUTE
			if truthy(value) {
				req.Command = api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_MUTE
			}
		default:
			// ESPHome's API has no next, previous, seek, or source.
			return nil, unknownField(e, field)
		}
		return req, nil
	}
	return nil, fmt.Errorf("%s %q takes no commands", strings.ReplaceAll(e.Type, "_", " "), e.Name)
}

// Media player feature bits, which ESPHome reports in Home Assistant's
// numbering.
const (
	mediaVolumeSet = 4
	mediaMute      = 8
	mediaTurnOn    = 128
	mediaTurnOff   = 256
	mediaStop      = 4096
)

// mediaOptions are a media player's kind options. The API carries no next,
// previous, seek, or source, so those are always off. Firmware older than
// feature flags reports none, and every player it built took volume, mute,
// stop, and power.
func mediaOptions(m *api.ListEntitiesMediaPlayerResponse) map[string]any {
	opts := map[string]any{"skip": false, "seek": false, "source": false,
		"volume": true, "mute": true, "stop": true, "power": true}
	if flags := m.GetFeatureFlags(); flags != 0 {
		opts["volume"] = flags&mediaVolumeSet != 0
		opts["mute"] = flags&mediaMute != 0
		opts["stop"] = flags&mediaStop != 0
		opts["power"] = flags&(mediaTurnOn|mediaTurnOff) != 0
	}
	return opts
}

// Light capability bits within ESPHome's colour modes.
const (
	capOnOff      = 1
	capBrightness = 2
	capWhite      = 4
	capColorTemp  = 8
	capColdWarm   = 16
	capRGB        = 32
)

// lightCaps ORs the capability bits of every colour mode a light supports.
func lightCaps(m *api.ListEntitiesLightResponse) uint32 {
	if m == nil {
		return capOnOff
	}
	var caps uint32
	for _, mode := range m.SupportedColorModes {
		caps |= uint32(mode)
	}
	if m.LegacySupportsBrightness { //nolint:staticcheck // SA1019: older firmware
		caps |= capBrightness
	}
	if m.LegacySupportsRgb { //nolint:staticcheck // SA1019: older firmware
		caps |= capRGB
	}
	if m.LegacySupportsColorTemperature { //nolint:staticcheck // SA1019: older firmware
		caps |= capColorTemp
	}
	return caps
}

// lightEffects drops the "None" entry ESPHome lists first.
func lightEffects(effects []string) []string {
	var out []string
	for _, e := range effects {
		if !strings.EqualFold(e, "None") {
			out = append(out, e)
		}
	}
	return out
}

// binaryLabels words a binary sensor's states by its device class.
func binaryLabels(class string) map[string]any {
	switch class {
	case "door", "window", "garage_door", "opening":
		return map[string]any{"true": "Open", "false": "Closed"}
	case "motion", "occupancy", "presence":
		return map[string]any{"true": "Detected", "false": "Clear"}
	case "moisture":
		return map[string]any{"true": "Wet", "false": "Dry"}
	case "smoke", "gas", "carbon_monoxide":
		return map[string]any{"true": "Detected", "false": "Clear"}
	case "connectivity":
		return map[string]any{"true": "Connected", "false": "Disconnected"}
	case "problem":
		return map[string]any{"true": "Problem", "false": "OK"}
	}
	return nil
}

// climateModeName and its friends render ESPHome's enums as the lower-case
// names the climate kind uses.
func climateModeName(m api.ClimateMode) string {
	return enumName(api.ClimateMode_name[int32(m)], "CLIMATE_MODE_")
}

func climateModeValue(name string) (api.ClimateMode, bool) {
	v, ok := api.ClimateMode_value["CLIMATE_MODE_"+strings.ToUpper(name)]
	return api.ClimateMode(v), ok
}

func climateActionName(a api.ClimateAction) string {
	return enumName(api.ClimateAction_name[int32(a)], "CLIMATE_ACTION_")
}

func climateFanName(f api.ClimateFanMode) string {
	return enumName(api.ClimateFanMode_name[int32(f)], "CLIMATE_FAN_")
}

func climateFanValue(name string) (api.ClimateFanMode, bool) {
	v, ok := api.ClimateFanMode_value["CLIMATE_FAN_"+strings.ToUpper(name)]
	return api.ClimateFanMode(v), ok
}

// climateFanModes lists a climate entity's fan modes, standard then custom.
func climateFanModes(m *api.ListEntitiesClimateResponse) []string {
	var out []string
	for _, f := range m.SupportedFanModes {
		out = append(out, climateFanName(f))
	}
	return append(out, m.SupportedCustomFanModes...)
}

// enumName strips an enum's prefix and lower-cases the rest.
func enumName(full, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(full, prefix))
}

// choices renders values as a kind's choices option.
func choices(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, map[string]any{"value": v, "label": v})
	}
	return out
}

// rgbHex renders ESPHome's 0 to 1 colour channels as hex.
func rgbHex(r, g, b float32) string {
	c := func(v float32) int { return int(math.Round(math.Max(0, math.Min(1, float64(v))) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", c(r), c(g), c(b))
}

// hexRGB reads a #rrggbb colour as 0 to 1 channels.
func hexRGB(hex string) (float32, float32, float32, error) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("colour %q is not #rrggbb", hex)
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("colour %q is not #rrggbb", hex)
	}
	return float32(n>>16&0xff) / 255, float32(n>>8&0xff) / 255, float32(n&0xff) / 255, nil
}

// truthy reads a coerced boolean.
func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// number reads a coerced number.
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

// unknownField names a field an entity cannot be told.
func unknownField(e Entity, field string) error {
	return fmt.Errorf("%s %q has no %q to set", strings.ReplaceAll(e.Type, "_", " "), e.Name, field)
}

// finite reports whether a reading is a number JSON can carry. A node sends
// NaN for a reading it does not have and an infinity for one its own
// arithmetic overflowed.
func finite(f float32) bool {
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}

// widen converts a float32 from the native API to the float64 with the same
// shortest decimal form. A direct conversion keeps the float32 rounding error,
// so 0.1 would read as 0.10000000149011612.
func widen(f float32) float64 {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	exact, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', -1, 32), 64)
	if err != nil {
		return v
	}
	return exact
}
