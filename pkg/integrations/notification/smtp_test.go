package notification_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSMTPEnvelopeAndMIME pins the SMTP command and MIME boundary with a local
// protocol peer, including refusal of a subject that could inject headers.
func TestSMTPEnvelopeAndMIME(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	envelope, content := make(chan []string, 1), make(chan string, 1)
	peerErrors := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			peerErrors <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "220 localhost SMTP\r\n")
		reader := bufio.NewReader(conn)
		var recipients []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				peerErrors <- err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_, _ = io.WriteString(conn, "250 localhost\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"):
				assert.Equal(t, "MAIL FROM:<sender@example.com>\r\n", line)
				_, _ = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "RCPT TO:"):
				recipients = append(recipients, line)
				_, _ = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "DATA"):
				_, _ = io.WriteString(conn, "354 send data\r\n")
				var data strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						peerErrors <- err
						return
					}
					if line == ".\r\n" {
						break
					}
					data.WriteString(line)
				}
				envelope <- recipients
				content <- data.String()
				_, _ = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				_, _ = io.WriteString(conn, "221 closed\r\n")
				peerErrors <- nil
				return
			}
		}
	}()
	raw, err := json.Marshal(notification.Config{Service: "mail", Credentials: map[string]string{"smtp_host": listener.Addr().String(), "sender_address": "sender@example.com"}, Endpoints: []notification.Endpoint{{ID: "first", Label: "First", Target: "first@example.com"}, {ID: "second", Label: "Second", Target: "second@example.com"}}})
	require.NoError(t, err)
	instance, err := notification.New(integrations.ConnModel{ID: uuid.New(), Config: raw}, integrations.Deps{})
	require.NoError(t, err)
	driver := instance.(*notification.Driver)
	require.NoError(t, driver.Start(context.Background()))
	defer driver.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, driver.Send(ctx, "second", "Room temperature", "Value 21"))
	require.NoError(t, <-peerErrors)
	assert.Equal(t, []string{"RCPT TO:<second@example.com>\r\n"}, <-envelope)
	message, err := mail.ReadMessage(strings.NewReader(<-content))
	require.NoError(t, err)
	recipients, err := message.Header.AddressList("To")
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	assert.Equal(t, "second@example.com", recipients[0].Address)
	assert.Equal(t, "Room temperature", message.Header.Get("Subject"))
	body, err := io.ReadAll(message.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Value 21")
	assert.ErrorContains(t, driver.Send(ctx, "second", "Room\r\nBcc: injected@example.com", "Value 21"), "subject must not contain line breaks")
}
