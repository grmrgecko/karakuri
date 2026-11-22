package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventRoutes covers FR-008 and SC-002 over REST: a reader without audit
// never sees security entries and is refused asking for them, an
// administrator sees both, and a user without the read permission sees
// nothing.
func TestEventRoutes(t *testing.T) {
	ts, admin := setupTestServer(t)
	read := recordEvents(t)
	events.Record(events.Entry{Category: models.EventDevice, Subject: events.Subject{Kind: "control", ID: "c1", Name: "Porch"}, Message: "Porch set to on"})
	events.Record(events.Entry{Category: models.EventSecurity, Level: models.EventWarning, Message: "Failed sign-in for admin"})
	read()

	reader := sessionWith(t, "eventreader", permissions.PermEventsRead)
	status, resp := doJSON(t, ts, reader, http.MethodGet, "/api/events", nil)
	require.Equalf(t, http.StatusOK, status, "%v", resp)
	list := resp["data"].(map[string]any)["events"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, "Porch set to on", list[0].(map[string]any)["message"])

	status, _ = doJSON(t, ts, reader, http.MethodGet, "/api/events?category=security", nil)
	assert.Equal(t, http.StatusForbidden, status)

	status, resp = doJSON(t, ts, admin, http.MethodGet, "/api/events?category=security", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, resp["data"].(map[string]any)["events"], 1)

	status, resp = doJSON(t, ts, admin, http.MethodGet, "/api/events?subject=control:c1", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, resp["data"].(map[string]any)["events"], 1)

	nobody := sessionWith(t, "eventnobody")
	status, _ = doJSON(t, ts, nobody, http.MethodGet, "/api/events", nil)
	assert.Equal(t, http.StatusForbidden, status)

	status, _ = doJSON(t, ts, reader, http.MethodPut, "/api/events/settings", map[string]any{"max_rows": 50000})
	assert.Equal(t, http.StatusForbidden, status, "changing the settings needs write")
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/events/settings", map[string]any{"max_rows": 5})
	assert.Equalf(t, http.StatusBadRequest, status, "a limit below 10,000 is refused: %v", resp)
}
