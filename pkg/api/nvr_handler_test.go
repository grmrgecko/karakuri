package api_test

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate/frigatetest"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestNVRRoutesFollowDashboardBindings covers the NVR relays: a public
// dashboard with a camera widget on doorbell and an event list on every
// camera of a second dashboard. A viewer of the first reads doorbell's
// snapshot and stream and its events and media, and nothing of garage; a
// viewer of the second reads every camera's events. A camera receiving no
// frames answers 503.
func TestNVRRoutesFollowDashboardBindings(t *testing.T) {
	t.Cleanup(frigate.SetTimingForTest(20*time.Millisecond, time.Second))
	ts, bearer := setupTestServer(t)
	config.C.Security.EncryptionKey = testEncryptionKey
	integrations.RegisterKind(frigate.KindDef())
	mgr := connmgr.New(nil, nil)
	connops.SetManager(mgr)
	t.Cleanup(func() {
		connops.SetManager(nil)
		mgr.Stop()
	})
	fake := frigatetest.New()
	t.Cleanup(fake.Close)
	doorEvent, garageEvent := "1793910167.5-door01", "1793910100.1-gara01"
	fake.AddEvent(map[string]any{"id": garageEvent, "camera": "garage", "label": "person", "zones": []string{},
		"start_time": 1793910100.1, "has_clip": true, "has_snapshot": true, "data": map[string]any{"score": 0.7}})
	fake.AddEvent(map[string]any{"id": doorEvent, "camera": "doorbell", "label": "person", "zones": []string{"porch"},
		"start_time": 1793910167.5, "has_clip": true, "has_snapshot": true, "data": map[string]any{"score": 0.9}})

	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Camera", PackageName: "camera", TagName: "camera-widget", Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."}},
		{Name: "NVR Events", PackageName: "nvr-events", TagName: "nvr-events-widget", Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."}},
	}))
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Frigate", "kind": "frigate", "config": map[string]any{"url": fake.URL},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status)
	ids := map[string]any{}
	for _, item := range resp["data"].([]any) {
		w := item.(map[string]any)
		ids[w["package_name"].(string)] = w["id"]
	}
	dashboard := func(name, pkg, camera string) string {
		status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": name, "is_public": true})
		require.Equal(t, http.StatusCreated, status)
		id := resp["data"].(map[string]any)["id"].(string)
		status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+id+"/widgets", map[string]any{
			"widget_id": ids[pkg], "x": 0, "y": 0, "width": 3, "height": 3,
			"config": `{"source":"nvr","displayOnly":true,"connectionId":"` + connID + `","camera":"` + camera + `"}`,
		})
		require.Equalf(t, http.StatusCreated, status, "placement: %v", resp)
		return id
	}
	hall := dashboard("Hall", "camera", "doorbell")
	wall := dashboard("Wall", "nvr-events", "*")

	get := func(dash, path string) (int, string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set(middleware.DashboardHeader, dash)
		res, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body), res.Header
	}
	base := "/api/connections/" + connID + "/nvr"

	status, body, _ := get(hall, base+"/cameras/doorbell/snapshot?height=180")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, string(frigatetest.JPEG), body)
	status, _, _ = get(hall, base+"/cameras/garage/snapshot")
	assert.Equal(t, http.StatusForbidden, status, "a camera the dashboard does not show stays out of reach")
	status, _, _ = get(wall, base+"/cameras/garage/snapshot")
	assert.Equal(t, http.StatusOK, status, "an all-cameras binding reaches every camera")

	status, resp = doAnon(t, ts, hall, http.MethodGet, base+"/events?limit=10", nil)
	require.Equalf(t, http.StatusOK, status, "events: %v", resp)
	events := resp["data"].(map[string]any)["events"].([]any)
	require.Len(t, events, 1, "a viewer sees only the cameras the dashboard shows")
	assert.Equal(t, "doorbell", events[0].(map[string]any)["camera"])
	status, resp = doAnon(t, ts, wall, http.MethodGet, base+"/events", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, resp["data"].(map[string]any)["events"].([]any), 2)

	status, _, _ = get(hall, base+"/events/"+doorEvent+"/thumbnail")
	assert.Equal(t, http.StatusOK, status)
	status, _, _ = get(hall, base+"/events/"+garageEvent+"/snapshot")
	assert.Equal(t, http.StatusForbidden, status, "an event's media follows its camera")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+base+"/events/"+doorEvent+"/clip", nil)
	req.Header.Set(middleware.DashboardHeader, hall)
	req.Header.Set("Range", "bytes=0-9")
	res, err := ts.Client().Do(req)
	require.NoError(t, err)
	clip, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, http.StatusPartialContent, res.StatusCode, "a clip honours Range")
	assert.Equal(t, "0123456789", string(clip))

	// The stream relays whole frames under karakuri's own boundary.
	sreq, _ := http.NewRequest(http.MethodGet, ts.URL+base+"/cameras/doorbell/stream?height=360", nil)
	sreq.Header.Set(middleware.DashboardHeader, hall)
	sres, err := ts.Client().Do(sreq)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sres.Header.Get("Content-Type"), "multipart/x-mixed-replace"))
	line, err := bufio.NewReader(sres.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "--karakuriframe\r\n", line)
	sres.Body.Close()

	fake.SetFPS("doorbell", 0)
	require.Eventually(t, func() bool {
		status, _, _ := get(hall, base+"/cameras/doorbell/snapshot")
		return status == http.StatusServiceUnavailable
	}, 2*time.Second, 20*time.Millisecond, "a camera receiving no frames answers 503")
}
