package scriptsdk_test

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
	"github.com/stretchr/testify/assert"
)

// TestValidateCallContract pairs a fully valid computed call with rejections at
// every admission boundary: closed config, direct value parameters, retry and
// identity options, and complete typed messages. Dynamic ports and binary data
// have positive cases beside the invalid ones so the checks cannot pass merely
// by rejecting every nontrivial call.
func TestValidateCallContract(t *testing.T) {
	meta := flow.NodeMeta{
		Type:            "dynamic-codec",
		DynamicPorts:    true,
		Retryable:       true,
		InvocationState: flow.InvocationStateStateful,
		Inputs: []flow.PortSpec{
			{ID: "in", Type: flow.PortString},
			{ID: "data", Type: flow.PortBinary, Optional: true},
		},
		Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortBinary}},
		ConfigSpec: []flow.FieldSpec{
			{ID: "mode", Type: flow.FieldSelect, Required: true, Options: []flow.Option{{Value: "text"}, {Value: "hex"}}},
			{ID: "count", Type: flow.FieldNumber, Required: true},
			{ID: "label", Type: flow.FieldString},
			{ID: "source", Type: flow.FieldJSON, Port: flow.PortBinary, Required: true},
		},
	}
	validCall := func() nodeapi.NodeCall {
		return nodeapi.NodeCall{
			Type:   meta.Type,
			Config: json.RawMessage(`{"mode":"text","count":2,"label":"reading"}`),
			Inputs: map[string]*nodeapi.Msg{
				"in":   {Payload: "hello"},
				"data": {Payload: "aGVsbG8="},
			},
			Options: nodeapi.NodeCallOptions{
				ID: "stable-invocation",
				Retry: &flow.RetryPolicy{
					MaxAttempts: 3, DelayMs: 1, Backoff: 2, MaxDelayMs: 10, Jitter: 0.1,
				},
				Params: map[string]any{"source": "aGVsbG8="},
			},
		}
	}
	tests := []struct {
		name     string
		edit     func(*nodeapi.NodeCall)
		editMeta func(*flow.NodeMeta)
		pure     bool
		wantErr  string
	}{
		{name: "valid computed call"},
		{name: "valid Go binary and base64 forms", edit: func(call *nodeapi.NodeCall) {
			call.Options.Params["source"] = []byte("hello")
			call.Inputs["data"].Payload = []byte("hello")
		}},
		{name: "valid audited stateless call without ID", edit: func(call *nodeapi.NodeCall) {
			call.Options.ID = ""
		}, editMeta: func(m *flow.NodeMeta) { m.InvocationState = flow.InvocationStateStateless }},
		{name: "valid pure call", pure: true, edit: func(call *nodeapi.NodeCall) {
			call.Options.Retry = nil
		}, editMeta: func(m *flow.NodeMeta) { m.Pure = true }},
		{name: "config argument is required", edit: func(call *nodeapi.NodeCall) {
			call.Config = nil
		}, wantErr: "config is required"},
		{name: "config null is not an object", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`null`)
		}, wantErr: "not null"},
		{name: "unknown config", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"text","count":2,"mystery":true}`)
		}, wantErr: `unknown config field "mystery"`},
		{name: "missing required config", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"text"}`)
		}, wantErr: `required config field "count" is missing`},
		{name: "wrong config number type", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"text","count":"two"}`)
		}, wantErr: `config field "count" must be a number`},
		{name: "wrong config string type", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"text","count":2,"label":4}`)
		}, wantErr: `config field "label" must be a string`},
		{name: "invalid config choice", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"other","count":2}`)
		}, wantErr: `config field "mode" value "other" is not a valid option`},
		{name: "permanent parameter cannot hide in config", edit: func(call *nodeapi.NodeCall) {
			call.Config = json.RawMessage(`{"mode":"text","count":2,"source":"aGVsbG8="}`)
			delete(call.Options.Params, "source")
		}, wantErr: "must be supplied through options.params"},
		{name: "unknown parameter", edit: func(call *nodeapi.NodeCall) {
			call.Options.Params["mystery"] = true
		}, wantErr: `unknown parameter "mystery"`},
		{name: "missing required parameter", edit: func(call *nodeapi.NodeCall) {
			delete(call.Options.Params, "source")
		}, wantErr: `required parameter "source" is missing`},
		{name: "invalid binary parameter", edit: func(call *nodeapi.NodeCall) {
			call.Options.Params["source"] = "not base64"
		}, wantErr: "standard base64"},
		{name: "missing stateful identity", edit: func(call *nodeapi.NodeCall) {
			call.Options.ID = ""
		}, wantErr: "requires a non-empty options.id"},
		{name: "unknown state also requires identity", edit: func(call *nodeapi.NodeCall) {
			call.Options.ID = ""
		}, editMeta: func(m *flow.NodeMeta) { m.InvocationState = flow.InvocationStateUnknown }, wantErr: "invocation state is unknown"},
		{name: "invalid retry", edit: func(call *nodeapi.NodeCall) {
			call.Options.Retry.MaxAttempts = 0
		}, wantErr: "max attempts must be between 1 and 1000"},
		{name: "retry on nonretryable type", editMeta: func(m *flow.NodeMeta) {
			m.Retryable = false
		}, wantErr: "does not accept a retry policy"},
		{name: "impure call in pure evaluation", pure: true, wantErr: "cannot be called during pure evaluation"},
		{name: "retry in pure evaluation", pure: true, editMeta: func(m *flow.NodeMeta) {
			m.Pure = true
		}, wantErr: "retry options are not available"},
		{name: "exact type cannot fall back", edit: func(call *nodeapi.NodeCall) {
			call.Type = "other-codec"
		}, wantErr: "does not match catalog type"},
		{name: "missing required input", edit: func(call *nodeapi.NodeCall) {
			delete(call.Inputs, "in")
		}, wantErr: `required input "in" is missing`},
		{name: "unknown input", edit: func(call *nodeapi.NodeCall) {
			call.Inputs["mystery"] = &nodeapi.Msg{Payload: "x"}
		}, wantErr: `unknown input port "mystery"`},
		{name: "parameter is not a message input", edit: func(call *nodeapi.NodeCall) {
			call.Inputs["param:source"] = &nodeapi.Msg{Payload: "aGVsbG8="}
		}, wantErr: "supply it through options.params"},
		{name: "wrong input payload type", edit: func(call *nodeapi.NodeCall) {
			call.Inputs["in"].Payload = 42
		}, wantErr: "must be a string"},
		{name: "invalid binary input", edit: func(call *nodeapi.NodeCall) {
			call.Inputs["data"].Payload = "not base64"
		}, wantErr: "standard base64"},
		{name: "nil message is not a message object", edit: func(call *nodeapi.NodeCall) {
			call.Inputs["data"] = nil
		}, wantErr: "complete message object"},
	}
	for _, tc := range tests {
		callMeta := meta
		call := validCall()
		if tc.editMeta != nil {
			tc.editMeta(&callMeta)
		}
		if tc.edit != nil {
			tc.edit(&call)
		}
		err := scriptsdk.ValidateCall(callMeta, call, tc.pure)
		if tc.wantErr == "" {
			assert.NoErrorf(t, err, "the positive boundary must remain callable for %s", tc.name)
			continue
		}
		assert.ErrorContainsf(t, err, tc.wantErr, "validation mismatch for %s", tc.name)
	}
}
