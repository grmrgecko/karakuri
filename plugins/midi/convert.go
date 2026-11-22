package main

import (
	"encoding/base64"
	"fmt"

	"gitlab.com/gomidi/midi/v2"
)

// Message type names as they appear in payloads. Channels are 1 to 16 on this
// surface and 0 to 15 on the wire; the conversion happens here so every flow
// and dashboard reads the numbering printed on the hardware.
const (
	typeNoteOn         = "note_on"
	typeNoteOff        = "note_off"
	typeControlChange  = "control_change"
	typeProgramChange  = "program_change"
	typePitchBend      = "pitch_bend"
	typeAfterTouch     = "aftertouch"
	typePolyAfterTouch = "poly_aftertouch"
	typeSysEx          = "sysex"
)

// decodeMessage converts a received MIDI message into the trigger's payload
// shape, or nil for messages this plugin does not surface (realtime and
// system common traffic). A note-on with velocity 0 is decoded literally;
// devices that mean note-off by it say so in their own payloads.
func decodeMessage(msg midi.Message) map[string]any {
	var ch, key, vel, pressure, controller, value, program uint8
	var rel int16
	var abs uint16
	var data []byte
	switch {
	case msg.GetNoteOn(&ch, &key, &vel):
		return map[string]any{"type": typeNoteOn, "channel": int(ch) + 1, "note": int(key), "velocity": int(vel)}
	case msg.GetNoteOff(&ch, &key, &vel):
		return map[string]any{"type": typeNoteOff, "channel": int(ch) + 1, "note": int(key), "velocity": int(vel)}
	case msg.GetControlChange(&ch, &controller, &value):
		return map[string]any{"type": typeControlChange, "channel": int(ch) + 1, "controller": int(controller), "value": int(value)}
	case msg.GetProgramChange(&ch, &program):
		return map[string]any{"type": typeProgramChange, "channel": int(ch) + 1, "program": int(program)}
	case msg.GetPitchBend(&ch, &rel, &abs):
		return map[string]any{"type": typePitchBend, "channel": int(ch) + 1, "value": int(rel)}
	case msg.GetAfterTouch(&ch, &pressure):
		return map[string]any{"type": typeAfterTouch, "channel": int(ch) + 1, "pressure": int(pressure)}
	case msg.GetPolyAfterTouch(&ch, &key, &pressure):
		return map[string]any{"type": typePolyAfterTouch, "channel": int(ch) + 1, "note": int(key), "pressure": int(pressure)}
	case msg.Is(midi.SysExMsg) && msg.GetSysEx(&data):
		return map[string]any{"type": typeSysEx, "data": base64.StdEncoding.EncodeToString(data)}
	default:
		return nil
	}
}

// encodeMessage converts one payload object into a MIDI message, the exact
// inverse of decodeMessage so a midi-in payload routes straight back out.
func encodeMessage(v any) (midi.Message, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a MIDI message is an object with a type field, not a %T", v)
	}
	typ, _ := obj["type"].(string)

	if typ == typeSysEx {
		raw, _ := obj["data"].(string)
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("sysex data is not valid base64: %w", err)
		}
		// A byte with the high bit set is a status byte and ends the SysEx on
		// the wire, so the data is the body alone: the F0 and F7 framing is
		// added here.
		for i, b := range data {
			if b > 0x7f {
				return nil, fmt.Errorf("sysex data byte %d is 0x%02X; data bytes are 0x00 to 0x7F, without the F0 and F7 framing", i, b)
			}
		}
		return midi.SysEx(data), nil
	}

	ch, err := intField(obj, "channel", 1, 16, 1)
	if err != nil {
		return nil, err
	}
	wireCh := uint8(ch - 1)

	switch typ {
	case typeNoteOn:
		note, err := intField(obj, "note", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		vel, err := intField(obj, "velocity", 0, 127, 127)
		if err != nil {
			return nil, err
		}
		return midi.NoteOn(wireCh, uint8(note), uint8(vel)), nil
	case typeNoteOff:
		note, err := intField(obj, "note", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		vel, err := intField(obj, "velocity", 0, 127, 0)
		if err != nil {
			return nil, err
		}
		return midi.NoteOffVelocity(wireCh, uint8(note), uint8(vel)), nil
	case typeControlChange:
		controller, err := intField(obj, "controller", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		value, err := intField(obj, "value", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		return midi.ControlChange(wireCh, uint8(controller), uint8(value)), nil
	case typeProgramChange:
		program, err := intField(obj, "program", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		return midi.ProgramChange(wireCh, uint8(program)), nil
	case typePitchBend:
		value, err := intField(obj, "value", -8192, 8191, -1)
		if err != nil {
			return nil, err
		}
		return midi.Pitchbend(wireCh, int16(value)), nil
	case typeAfterTouch:
		pressure, err := intField(obj, "pressure", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		return midi.AfterTouch(wireCh, uint8(pressure)), nil
	case typePolyAfterTouch:
		note, err := intField(obj, "note", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		pressure, err := intField(obj, "pressure", 0, 127, -1)
		if err != nil {
			return nil, err
		}
		return midi.PolyAfterTouch(wireCh, uint8(note), uint8(pressure)), nil
	default:
		return nil, fmt.Errorf("unknown MIDI message type %q", typ)
	}
}

// encodeMessages converts a payload into the messages to send: an object is
// one message, a list sends each in order, which is how a chord or a
// note-on/note-off pair leaves in one execution.
func encodeMessages(payload any) ([]midi.Message, error) {
	list, ok := payload.([]any)
	if !ok {
		list = []any{payload}
	}
	out := make([]midi.Message, 0, len(list))
	for i, v := range list {
		msg, err := encodeMessage(v)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, msg)
	}
	return out, nil
}

// intField reads an integer field with bounds. A def of -1 marks the field
// required; the caller's bounds never include -1 for those.
func intField(obj map[string]any, key string, min, max, def int) (int, error) {
	raw, ok := obj[key]
	if !ok {
		if def == -1 {
			return 0, fmt.Errorf("%q is required", key)
		}
		return def, nil
	}
	f, ok := raw.(float64)
	if !ok || f != float64(int(f)) {
		return 0, fmt.Errorf("%q must be a whole number", key)
	}
	n := int(f)
	if n < min || n > max {
		return 0, fmt.Errorf("%q must be between %d and %d", key, min, max)
	}
	return n, nil
}
