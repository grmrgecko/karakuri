package gui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInteractionAbsentFieldDefaults verifies drag and placement settings a
// saved configuration omits keep the defaults the editor shows: a half-second
// drag, and a restore before placement. It pins the regression where both
// decoded as zero values, so a flow saved without them dragged instantly and
// placed a maximized window without restoring it, while explicit zero and
// false still have to be honored.
func TestInteractionAbsentFieldDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		duration     time.Duration
		restore      bool
	}{
		{"omitted fields", `{"width":640,"height":480}`, 500 * time.Millisecond, true},
		{"explicit zero and false", `{"width":640,"height":480,"duration":0,"restore":false}`, 0, false},
		{"explicit values", `{"width":640,"height":480,"duration":2,"restore":true}`, 2 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg interactionConfig
			require.NoError(t, json.Unmarshal([]byte(tc.config), &cfg))

			drag, err := cfg.dragRequest()
			require.NoError(t, err, "the drag settings are valid")
			assert.Equal(t, tc.duration, drag.Duration, "an omitted duration is the default, and a written one is kept")

			placement, err := cfg.placement()
			require.NoError(t, err, "the placement settings are valid")
			assert.Equal(t, tc.restore, placement.Restore, "an omitted restore is on, and a written one is kept")
		})
	}
}
