package main

import (
	"fmt"
	"math"
	"strconv"
)

// channelValues converts a message payload into DMX channel levels, 1-based.
// An object maps channel numbers to levels ({"1": 255, "7": 40}), a list
// writes consecutive channels starting at startChannel, and a single value
// writes startChannel alone. Levels are 0 to 255; fractions round, and true
// and false read as full and off so a switch payload drives a relay channel
// directly.
func channelValues(payload any, startChannel int) (map[int]byte, error) {
	if startChannel < 1 || startChannel > 512 {
		return nil, fmt.Errorf("start channel must be between 1 and 512")
	}
	switch v := payload.(type) {
	case nil:
		return nil, fmt.Errorf("payload holds no channel values")
	case map[string]any:
		out := make(map[int]byte, len(v))
		for key, raw := range v {
			ch, err := strconv.Atoi(key)
			if err != nil || ch < 1 || ch > 512 {
				return nil, fmt.Errorf("%q is not a DMX channel; channels are 1 to 512", key)
			}
			level, err := channelLevel(raw)
			if err != nil {
				return nil, fmt.Errorf("channel %d: %w", ch, err)
			}
			out[ch] = level
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("payload holds no channel values")
		}
		return out, nil
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("payload holds no channel values")
		}
		if startChannel+len(v)-1 > 512 {
			return nil, fmt.Errorf("%d values starting at channel %d run past channel 512", len(v), startChannel)
		}
		out := make(map[int]byte, len(v))
		for i, raw := range v {
			level, err := channelLevel(raw)
			if err != nil {
				return nil, fmt.Errorf("channel %d: %w", startChannel+i, err)
			}
			out[startChannel+i] = level
		}
		return out, nil
	default:
		level, err := channelLevel(v)
		if err != nil {
			return nil, err
		}
		return map[int]byte{startChannel: level}, nil
	}
}

// channelLevel converts one payload value to a DMX level byte.
func channelLevel(v any) (byte, error) {
	switch t := v.(type) {
	case bool:
		if t {
			return 255, nil
		}
		return 0, nil
	case float64:
		n := math.Round(t)
		if n < 0 || n > 255 {
			return 0, fmt.Errorf("level %v is outside 0 to 255", t)
		}
		return byte(n), nil
	case int:
		if t < 0 || t > 255 {
			return 0, fmt.Errorf("level %d is outside 0 to 255", t)
		}
		return byte(t), nil
	case int64:
		if t < 0 || t > 255 {
			return 0, fmt.Errorf("level %d is outside 0 to 255", t)
		}
		return byte(t), nil
	default:
		return 0, fmt.Errorf("cannot read a %T as a DMX level; levels are numbers 0 to 255 or booleans", v)
	}
}

// channelsPayload converts a received frame into the trigger's message
// payload shape: the full frame as numbers plus the changed channels keyed by
// their 1-based number, since JSON object keys are strings.
func channelsPayload(evt ReceiveEvent) map[string]any {
	channels := make([]any, len(evt.Channels))
	for i, v := range evt.Channels {
		channels[i] = int(v)
	}
	changed := make(map[string]any, len(evt.Changed))
	for ch, v := range evt.Changed {
		changed[strconv.Itoa(ch)] = int(v)
	}
	return map[string]any{
		"universe": evt.Universe,
		"channels": channels,
		"changed":  changed,
	}
}
