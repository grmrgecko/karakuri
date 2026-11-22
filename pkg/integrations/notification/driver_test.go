package notification_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebhookCancellation pins cancellation at the real HTTP socket boundary,
// where a provider may have read the message before the deadline expires.
func TestWebhookCancellation(t *testing.T) {
	arrived, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			return
		}
		arrived <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer receiver.Close()
	raw, err := json.Marshal(notification.Config{Service: "http", Credentials: map[string]string{}, Endpoints: []notification.Endpoint{{ID: "room", Label: "Room", Target: receiver.URL}}})
	require.NoError(t, err)
	instance, err := notification.New(integrations.ConnModel{ID: uuid.New(), Config: raw}, integrations.Deps{})
	require.NoError(t, err)
	driver := instance.(*notification.Driver)
	require.NoError(t, driver.Start(context.Background()))
	defer driver.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- driver.Send(ctx, "room", "Subject", "Body") }()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("The webhook did not receive the request.")
	}
	assert.ErrorIs(t, <-result, context.DeadlineExceeded)
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("The expired send did not close its HTTP request.")
	}
}

// TestNotifyInventory pins notify v1.6.0's 33 packages and 36 adapter variants,
// so adding or dropping a provider requires an explicit catalog decision. The
// dashboard service is Karakuri's own and the one entry outside the upstream
// inventory.
func TestNotifyInventory(t *testing.T) {
	raw, err := os.ReadFile("testdata/notify-v1.6.0.json")
	require.NoError(t, err)
	var inventory struct {
		Variants []string `json:"variants"`
	}
	require.NoError(t, json.Unmarshal(raw, &inventory))
	var actual []string
	for _, service := range notification.Services() {
		if service.ID == "dashboard" {
			continue
		}
		actual = append(actual, service.ID)
	}
	assert.ElementsMatch(t, inventory.Variants, actual)
}

// TestEmptySubjectAddsNoBlankLine pins the text of services that have one
// body for subject and message: a subject sits on its own first line, and no
// subject leaves the message alone. The subject is optional, and joining an
// empty one opened every chat message and SMS with a blank line and every
// syslog record with a stray ": ".
func TestEmptySubjectAddsNoBlankLine(t *testing.T) {
	posted := make(chan string, 1)
	mattermost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var post struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&post)
		posted <- post.Message
		_, _ = w.Write([]byte(`{"id":"post1"}`))
	}))
	defer mattermost.Close()
	logs, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer logs.Close()
	target, err := json.Marshal(map[string]string{"network": "udp", "address": logs.LocalAddr().String()})
	require.NoError(t, err)

	start := func(cfg notification.Config) *notification.Driver {
		raw, err := json.Marshal(cfg)
		require.NoError(t, err)
		instance, err := notification.New(integrations.ConnModel{ID: uuid.New(), Config: raw}, integrations.Deps{})
		require.NoError(t, err)
		driver := instance.(*notification.Driver)
		require.NoError(t, driver.Start(context.Background()))
		t.Cleanup(driver.Stop)
		return driver
	}
	chat := start(notification.Config{Service: "mattermost",
		Credentials: map[string]string{"server_url": mattermost.URL, "token": "secret"},
		Endpoints:   []notification.Endpoint{{ID: "room", Label: "Room", Target: "channel1"}}})
	syslog := start(notification.Config{Service: "syslog", Credentials: map[string]string{"tag": "house"},
		Endpoints: []notification.Endpoint{{ID: "log", Label: "Log", Target: string(target)}}})
	record := func() string {
		buf := make([]byte, 2048)
		require.NoError(t, logs.SetReadDeadline(time.Now().Add(3*time.Second)))
		n, _, err := logs.ReadFrom(buf)
		require.NoError(t, err)
		return string(buf[:n])
	}

	for _, tc := range []struct {
		name, subject, chat, log string
	}{
		{"a subject leads the message", "Alert", "Alert\nDoor open", "]: Alert: Door open\n"},
		{"no subject leaves the message alone", "", "Door open", "]: Door open\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, chat.Send(ctx, "room", tc.subject, "Door open"))
			assert.Equal(t, tc.chat, <-posted, "The chat body holds the subject line only when there is a subject.")
			require.NoError(t, syslog.Send(ctx, "log", tc.subject, "Door open"))
			got := record()
			assert.True(t, strings.HasSuffix(got, tc.log),
				"The syslog record %q should end with %q.", got, tc.log)
		})
	}
}
