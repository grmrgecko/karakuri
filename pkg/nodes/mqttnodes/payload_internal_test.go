package mqttnodes

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildPayloadSendsBytesRaw locks what a message payload becomes on the
// wire. MQTT In in binary mode hands a flow a []byte, and relaying it must
// publish those bytes: encoding/json turns a byte slice into a quoted base64
// string, which no device reading the topic can use. Text goes out as typed
// and structured values as JSON.
func TestBuildPayloadSendsBytesRaw(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    []byte
	}{
		{name: "bytes", payload: []byte{0x01, 0x02, 0xff}, want: []byte{0x01, 0x02, 0xff}},
		{name: "text", payload: "ON", want: []byte("ON")},
		{name: "object", payload: map[string]any{"state": "ON"}, want: []byte(`{"state":"ON"}`)},
	}
	for _, tc := range cases {
		got, err := buildPayload(nil, &outConfig{}, &engine.Msg{Payload: tc.payload})
		require.NoErrorf(t, err, "a %s payload must publish", tc.name)
		assert.Equalf(t, tc.want, got, "wire bytes for a %s payload", tc.name)
	}
}
