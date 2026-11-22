package mqttrelay

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderPublishBody covers what actually reaches a device: the literal
// payload path a row without a template still uses, and the templated path,
// where the expressions are the same ones a flow's Template node runs.
func TestRenderPublishBody(t *testing.T) {
	cases := []struct {
		name string
		in   PublishSpec
		want string
	}{
		{
			name: "literal payload passes through",
			in:   PublishSpec{Payload: `{"state":"ON"}`},
			want: `{"state":"ON"}`,
		},
		{
			name: "text template is the payload",
			in: PublishSpec{
				Template: "{{ value }}",
				Vars:     map[string]any{"value": "ON"},
			},
			want: "ON",
		},
		{
			name: "text template takes a conditional",
			in: PublishSpec{
				Template: "{{ on ? 'up' : 'down' }}",
				Vars:     map[string]any{"on": false},
			},
			want: "down",
		},
		{
			// A switch sends ON/OFF as a standard rather than a setting, so the
			// documented way to drive a device that wants 1/0 is the template.
			name: "the standard switch payload",
			in: PublishSpec{
				Template: "{{ value }}",
				Vars:     map[string]any{"value": "ON", "on": true},
			},
			want: "ON",
		},
		{
			name: "a switch retargeted at 1 and 0",
			in: PublishSpec{
				Template: "{{ on ? 1 : 0 }}",
				Vars:     map[string]any{"value": "OFF", "on": false},
			},
			want: "0",
		},
		{
			// A field holding one whole expression keeps that expression's type,
			// which is what lets the stored template stay valid JSON.
			name: "json template keeps each expression's type",
			in: PublishSpec{
				Format: "json",
				Template: `{
				  "state": "{{ value > 0 ? 'ON' : 'OFF' }}",
				  "brightness": "{{ round(value * 2.55) }}",
				  "band": "{{ value > 50 ? 'high' : value > 20 ? 'mid' : 'low' }}",
				  "label": "level {{ value }}",
				  "transition": 2
				}`,
				Vars: map[string]any{"value": 60},
			},
			want: `{"band":"high","brightness":153,"label":"level 60","state":"ON","transition":2}`,
		},
		{
			name: "json template fills nested documents and arrays",
			in: PublishSpec{
				Format:   "json",
				Template: `{"color":{"r":"{{ r }}"},"steps":["{{ r }}",2]}`,
				Vars:     map[string]any{"r": 12},
			},
			want: `{"color":{"r":12},"steps":[12,2]}`,
		},
		{
			// Undefined names read as nothing everywhere else in karakuri, and a
			// null in the payload is visible rather than silently omitted.
			name: "unknown variable renders null",
			in: PublishSpec{
				Format:   "json",
				Template: `{"a":"{{ nope }}"}`,
				Vars:     map[string]any{},
			},
			want: `{"a":null}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderPublishBody(context.Background(), c.in)
			require.NoError(t, err, "this command must render")
			assert.Equal(t, c.want, got.Payload, "this is what actually reaches the device")
		})
	}
}

// TestRenderPublishBodyScript covers the script command format: the variables a
// script reads, the two return shapes, and a script routing its own topic.
func TestRenderPublishBodyScript(t *testing.T) {
	cases := []struct {
		name      string
		in        PublishSpec
		want      string
		wantTopic string
	}{
		{
			// The case this format was added for: two IR codes behind one
			// binding, chosen by the direction pressed.
			name: "an infrared blaster with a code per direction",
			in: PublishSpec{
				Format: "script",
				Template: `if (!on) return "Samsung 0x707 0x98 1";
return { topic: "cmnd/obk1F39F2A9/IRSend", payload: "Samsung 0x707 0x99 1" };`,
				Vars: map[string]any{"value": "ON", "on": true},
			},
			want:      "Samsung 0x707 0x99 1",
			wantTopic: "cmnd/obk1F39F2A9/IRSend",
		},
		{
			name: "the same script on the off press",
			in: PublishSpec{
				Format: "script",
				Template: `if (!on) return "Samsung 0x707 0x98 1";
return { topic: "cmnd/obk1F39F2A9/IRSend", payload: "Samsung 0x707 0x99 1" };`,
				Vars: map[string]any{"value": "OFF", "on": false},
			},
			want: "Samsung 0x707 0x98 1",
		},
		{
			name: "an object payload is encoded as JSON",
			in: PublishSpec{
				Format:   "script",
				Template: `return { payload: { state: value, level: Math.round(previous * 2.55) } };`,
				Vars:     map[string]any{"value": "ON", "previous": 40},
			},
			want: `{"level":102,"state":"ON"}`,
		},
		{
			name: "a bare object with no payload key is the payload",
			in: PublishSpec{
				Format:   "script",
				Template: `return { state: value };`,
				Vars:     map[string]any{"value": "ON"},
			},
			want: `{"state":"ON"}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderPublishBody(context.Background(), c.in)
			require.NoError(t, err, "this script must run")
			assert.Equal(t, c.want, got.Payload, "this is what actually reaches the device")
			assert.Equal(t, c.wantTopic, got.Topic,
				"a script may name its own topic; returning a bare payload leaves the row's topic in place")
		})
	}
}

// TestRenderPublishBodyRejects covers what must fail at the press rather than
// reaching the device as something the format does not describe. A malformed
// JSON command is the case that matters most: published as text it would be
// accepted by the broker and silently ignored or misread by the device, so the
// press would appear to work while nothing happened.
func TestRenderPublishBodyRejects(t *testing.T) {
	cases := []struct {
		name string
		in   PublishSpec
	}{
		{
			name: "payload and template together",
			in:   PublishSpec{Payload: "ON", Template: "{{ value }}"},
		},
		{
			name: "json template that does not parse",
			in:   PublishSpec{Format: "json", Template: `{"state": }`},
		},
		{
			name: "expression that does not compile",
			in:   PublishSpec{Format: "json", Template: `{"state": "{{ ( }}"}`},
		},
		{
			name: "script that does not compile",
			in:   PublishSpec{Format: "script", Template: "return {"},
		},
		{
			name: "script that returns nothing",
			in:   PublishSpec{Format: "script", Template: "const unused = 1;"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderPublishBody(context.Background(), c.in)
			assert.Error(t, err,
				"this must fail at the press: published anyway, the broker would accept it and the device would ignore it")
		})
	}
}
