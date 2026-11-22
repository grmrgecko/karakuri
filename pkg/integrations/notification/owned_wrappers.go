package notification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	lark "github.com/go-lark/lark/v2"
	"github.com/jordan-wright/email"
	"github.com/nikoksr/notify"
)

// larkClient keeps SDK requests on the execution's private HTTP client.
type larkClient struct{ client *http.Client }

// syslogLineBreaks folds line breaks so one message stays one stream record.
var syslogLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

func (c larkClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.client.Do(req.WithContext(ctx))
}

// silentLarkLog prevents provider URLs and response bodies from entering logs.
type silentLarkLog struct{}

func (silentLarkLog) Log(context.Context, lark.LogLevel, string) {}
func (silentLarkLog) SetOutput(io.Writer)                        {}

// newLarkNotifier owns a lazy-token client instead of an inaccessible heartbeat.
func newLarkNotifier(ctx context.Context, client *http.Client, c map[string]string, target string, webhook bool) (notify.Notifier, func(), error) {
	options := []lark.BotOption{lark.WithHTTPClient(larkClient{client}), lark.WithLogger(silentLarkLog{}), lark.WithDomain(lark.DomainLark)}
	var bot *lark.Bot
	var receiver struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if webhook {
		bot = lark.NewNotificationBot(target, options...)
	} else {
		bot = lark.NewChatBot(c["app_id"], c["app_secret"], options...)
		if err := json.Unmarshal([]byte(target), &receiver); err != nil {
			return nil, nil, err
		}
	}
	return sendFn(func(ctx context.Context, subject, message string) error {
		post := lark.NewPostBuilder().Title(subject).TextTag(message, 1, false).Render()
		buffer := lark.NewMsgBuffer(lark.MsgPost).Post(post)
		if webhook {
			r, err := bot.PostNotification(ctx, buffer.Build())
			if err != nil {
				return err
			}
			if r.Code != 0 || r.StatusCode != 0 {
				return fmt.Errorf("lark webhook refused the message")
			}
			return nil
		}
		switch receiver.Type {
		case "chat_id":
			buffer.BindChatID(receiver.ID)
		case "open_id":
			buffer.BindOpenID(receiver.ID)
		case "user_id":
			buffer.BindUserID(receiver.ID)
		case "union_id":
			buffer.BindUnionID(receiver.ID)
		case "email":
			buffer.BindEmail(receiver.ID)
		}
		r, err := bot.PostMessage(ctx, buffer.Build())
		if err != nil {
			return err
		}
		if r.Code != 0 {
			return fmt.Errorf("lark refused the message")
		}
		return nil
	}), func() {}, nil
}

// newSMTPNotifier closes its socket on cancellation, including during SMTP I/O.
func newSMTPNotifier(ctx context.Context, c map[string]string, target string) (notify.Notifier, func(), error) {
	return sendFn(func(ctx context.Context, subject, message string) error {
		host, _, err := net.SplitHostPort(c["smtp_host"])
		if err != nil {
			return err
		}
		conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "tcp", c["smtp_host"])
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline := time.Now().Add(30 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer client.Close()
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
		if c["user_name"] != "" {
			authHost := c["auth_host"]
			if authHost == "" {
				authHost = host
			}
			if err = client.Auth(smtp.PlainAuth(c["identity"], c["user_name"], c["password"], authHost)); err != nil {
				return err
			}
		}
		sender, err := mail.ParseAddress(c["sender_address"])
		if err != nil {
			return err
		}
		receiver, err := mail.ParseAddress(target)
		if err != nil {
			return err
		}
		if err = client.Mail(sender.Address); err != nil {
			return err
		}
		if err = client.Rcpt(receiver.Address); err != nil {
			return err
		}
		e := email.NewEmail()
		e.From = c["sender_address"]
		e.To = []string{target}
		e.Subject = subject
		if c["body_format"] == "html" {
			e.HTML = []byte(message)
		} else {
			e.Text = []byte(message)
		}
		data, err := e.Bytes()
		if err != nil {
			return err
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		if _, err = writer.Write(data); err != nil {
			writer.Close()
			return err
		}
		if err = writer.Close(); err != nil {
			return err
		}
		return client.Quit()
	}), func() {}, nil
}

// newSyslogNotifier uses an owned deadline-bound socket on every platform.
func newSyslogNotifier(ctx context.Context, c map[string]string, target string) (notify.Notifier, func(), error) {
	var receiver struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(target), &receiver); err != nil {
		return nil, nil, err
	}
	priority := 13
	if c["priority"] != "" {
		n, err := strconv.Atoi(c["priority"])
		if err != nil || n < 0 || n > 191 {
			return nil, nil, fmt.Errorf("invalid syslog priority")
		}
		priority = n
	}
	tag := c["tag"]
	if tag == "" {
		tag = "karakuri"
	}
	return sendFn(func(ctx context.Context, subject, message string) error {
		conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, receiver.Network, receiver.Address)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline := time.Now().Add(30 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		host, _ := os.Hostname()
		message = strings.TrimSuffix(message, "\n")
		if receiver.Network == "tcp" || receiver.Network == "unix" {
			// Stream receivers end a record at each newline, so message text from
			// flow data must not be able to start a forged record.
			message = syslogLineBreaks.Replace(message)
		}
		if subject != "" {
			message = subject + ": " + message
		}
		text := fmt.Sprintf("<%d>%s %s %s[%d]: %s\n", priority, time.Now().Format(time.Stamp), host, tag, os.Getpid(), message)
		_, err = io.WriteString(conn, text)
		return err
	}), func() {}, nil
}
