package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/nikoksr/notify"
	"github.com/nikoksr/notify/service/amazonses"
	"github.com/nikoksr/notify/service/amazonsns"
	"github.com/nikoksr/notify/service/bark"
	"github.com/nikoksr/notify/service/discord"
	"github.com/nikoksr/notify/service/googlechat"
	notifyhttp "github.com/nikoksr/notify/service/http"
	"github.com/nikoksr/notify/service/line"
	"github.com/nikoksr/notify/service/mailgun"
	"github.com/nikoksr/notify/service/mailtrap"
	"github.com/nikoksr/notify/service/matrix"
	"github.com/nikoksr/notify/service/msteams"
	"github.com/nikoksr/notify/service/pagerduty"
	"github.com/nikoksr/notify/service/plivo"
	"github.com/nikoksr/notify/service/reddit"
	"github.com/nikoksr/notify/service/sendgrid"
	"github.com/nikoksr/notify/service/slack"
	"github.com/nikoksr/notify/service/textmagic"
	"github.com/nikoksr/notify/service/webpush"

	dingtalksdk "github.com/blinkbean/dingtalk"
	pushbulletsdk "github.com/cschomburg/go-pushbullet"
	"github.com/dghubble/oauth1"
	twittersdk "github.com/drswork/go-twitter/twitter"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	twiliosdk "github.com/kevinburke/twilio-go"
	"google.golang.org/api/option"
	"maunium.net/go/mautrix/id"
)

type sendFn func(context.Context, string, string) error

func (f sendFn) Send(ctx context.Context, subject, message string) error {
	return f(ctx, subject, message)
}

// oneBody joins subject and message for a service with a single text body.
// The subject is optional, and an empty one adds no line of its own.
func oneBody(subject, message string) string {
	if subject == "" {
		return message
	}
	return subject + "\n" + message
}

// roundTripper combines an upstream request deadline with execution cancellation.
// The cancellation link must live through response-body consumption.
type roundTripper struct {
	ctx  context.Context
	base http.RoundTripper
}
type boundBody struct {
	io.ReadCloser
	finish func()
}

func (b boundBody) Close() error { defer b.finish(); return b.ReadCloser.Close() }
func (r roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(r.ctx, cancel)
	finish := func() { stop(); cancel() }
	resp, err := r.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		finish()
		return nil, err
	}
	resp.Body = boundBody{ReadCloser: resp.Body, finish: finish}
	return resp, nil
}
func privateClient(ctx context.Context) *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		Transport:     roundTripper{ctx: ctx, base: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// newNotifier constructs only the previously authorized receiver.
func newNotifier(ctx context.Context, kind string, c map[string]string, target string) (notify.Notifier, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if target == "" {
		return nil, nil, errors.New("notification endpoint is required")
	}
	done := func() {}
	hc := privateClient(ctx)
	switch kind {
	case "amazonses":
		s, e := amazonses.New(c["access_key_id"], c["secret_key"], c["region"], c["sender_address"])
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "amazonsns":
		s, e := amazonsns.New(c["access_key_id"], c["secret_key"], c["region"])
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "bark":
		// Bark must not add its implicit default server.
		return bark.NewWithServers(c["device_key"], target), done, nil
	case "dingding":
		sdk := dingtalksdk.InitDingTalkWithSecret(target, c["secret"])
		return sendFn(func(ctx context.Context, subject, message string) error {
			return sdk.SendTextMessageWithCtx(ctx, oneBody(subject, message))
		}), done, nil
	case "discord":
		s := discord.New()
		client := discord.DefaultSession()
		client.Client = hc
		s.SetClient(client)
		var e error
		if c["auth_type"] == "oauth2" {
			e = s.AuthenticateWithOAuth2Token(c["token"])
		} else {
			e = s.AuthenticateWithBotToken(c["token"])
		}
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "fcm":
		return newFirebaseNotifier(ctx, hc, c, target)
	case "googlechat":
		ahc, e := authenticatedGoogleClient(ctx, hc, []byte(c["credentials_json"]), "https://www.googleapis.com/auth/chat.bot")
		if e != nil {
			return nil, nil, e
		}
		s, e := googlechat.NewWithContext(ctx, option.WithHTTPClient(ahc))
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "http":
		headers := http.Header{}
		if c["headers_json"] != "" {
			var values map[string]string
			if e := json.Unmarshal([]byte(c["headers_json"]), &values); e != nil {
				return nil, nil, e
			}
			for k, v := range values {
				headers.Set(k, v)
			}
		}
		method := c["method"]
		if method == "" {
			method = http.MethodPost
		}
		s := notifyhttp.New()
		s.WithClient(hc)
		s.AddReceivers(&notifyhttp.Webhook{URL: target, Method: method, Header: headers, ContentType: "application/json",
			BuildPayload: func(subject, message string) any { return map[string]string{"subject": subject, "message": message} },
		})
		return s, done, nil
	case "lark":
		return newLarkNotifier(ctx, hc, c, target, false)
	case "lark-webhook":
		return newLarkNotifier(ctx, hc, c, target, true)
	case "line":
		s, e := line.New(c["channel_secret"], c["channel_access_token"])
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "line-notify":
		return nil, nil, errors.New("LINE Notify ended service on March 31, 2025")
	case "mail":
		return newSMTPNotifier(ctx, c, target)
	case "mailgun":
		opts := []mailgun.Option{}
		if c["region"] == "eu" {
			opts = append(opts, mailgun.WithEurope())
		}
		s := mailgun.New(c["domain"], c["api_key"], c["sender_address"], opts...)
		s.AddReceivers(target)
		return s, done, nil
	case "mailtrap":
		opts := []mailtrap.Option{mailtrap.WithHTTPClient(hc), mailtrap.WithSenderName(c["sender_name"])}
		switch c["mode"] {
		case "bulk":
			opts = append(opts, mailtrap.WithBulkHost())
		case "sandbox":
			opts = append(opts, mailtrap.WithSandbox(c["inbox_id"]))
		}
		s := mailtrap.New(c["api_key"], c["sender_address"], opts...)
		if c["body_format"] == "text" {
			s.BodyFormat(mailtrap.PlainText)
		}
		s.AddReceivers(target)
		return s, done, nil
	case "matrix":
		s, e := matrix.New(id.UserID(c["user_id"]), id.RoomID(target), c["homeserver"], c["access_token"])
		if e != nil {
			return nil, nil, e
		}
		return s, done, nil
	case "mattermost":
		return newMattermostHTTPNotifier(ctx, hc, c, target)
	case "msteams":
		s := msteams.New()
		s.SetHTTPClient(hc)
		s.WithWrapText(c["wrap_text"] != "false")
		s.AddReceivers(target)
		return s, done, nil
	case "pagerduty":
		s, e := pagerduty.New(c["token"])
		if e != nil {
			return nil, nil, e
		}
		s.SetFromAddress(c["from_address"])
		if c["urgency"] != "" {
			s.SetUrgency(c["urgency"])
		}
		s.SetPriorityID(c["priority_id"])
		if c["notification_type"] != "" {
			s.SetNotificationType(c["notification_type"])
		}
		s.Config.AddReceivers(target)
		return s, done, nil
	case "plivo":
		s, e := plivo.New(&plivo.ClientOptions{AuthID: c["auth_id"], AuthToken: c["auth_token"], HTTPClient: hc}, &plivo.MessageOptions{Source: c["source"]})
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "pushbullet":
		sdk := pushbulletsdk.NewWithClient(c["api_token"], hc)
		return sendFn(func(ctx context.Context, subject, message string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			d, e := sdk.Device(target)
			if e != nil {
				return e
			}
			return d.PushNote(subject, message)
		}), done, nil
	case "pushbullet-sms":
		sdk := pushbulletsdk.NewWithClient(c["api_token"], hc)
		return sendFn(func(ctx context.Context, subject, message string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			d, e := sdk.Device(c["device_nickname"])
			if e != nil {
				return e
			}
			u, e := sdk.Me()
			if e != nil {
				return e
			}
			return sdk.PushSMS(u.Iden, d.Iden, target, oneBody(subject, message))
		}), done, nil
	case "pushover":
		return newPushoverHTTPNotifier(hc, c["app_token"], target), done, nil
	case "reddit":
		s, e := reddit.New(c["client_id"], c["client_secret"], c["user_name"], c["password"])
		if e != nil {
			return nil, nil, e
		}
		s.AddReceivers(target)
		return s, done, nil
	case "rocketchat":
		return newRocketChatHTTPNotifier(hc, c, target), done, nil
	case "sendgrid":
		s := sendgrid.New(c["api_key"], c["sender_address"], c["sender_name"])
		if c["body_format"] == "text" {
			s.BodyFormat(sendgrid.PlainText)
		}
		s.AddReceivers(target)
		return s, done, nil
	case "slack":
		s := slack.New(c["api_token"])
		s.AddReceivers(target)
		return s, done, nil
	case "syslog":
		return newSyslogNotifier(ctx, c, target)
	case "telegram":
		chatID, e := strconv.ParseInt(target, 10, 64)
		if e != nil {
			return nil, nil, e
		}
		bot, e := tgbotapi.NewBotAPIWithClient(c["api_token"], hc)
		if e != nil {
			return nil, nil, e
		}
		return sendFn(func(ctx context.Context, subject, message string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			msg := tgbotapi.NewMessage(chatID, oneBody(subject, message))
			msg.ParseMode = c["parse_mode"] // Per message, never notify.Telegram.SetParseMode.
			_, e := bot.Send(msg)
			return e
		}), done, nil
	case "textmagic":
		s := textmagic.New(c["user_name"], c["api_key"])
		s.AddReceivers(target)
		return s, done, nil
	case "twilio":
		sdk := twiliosdk.NewClient(c["account_sid"], c["auth_token"], hc)
		return sendFn(func(ctx context.Context, subject, message string) error {
			_, e := sdk.Messages.Create(ctx, url.Values{"From": {c["from_phone_number"]}, "To": {target}, "Body": {oneBody(subject, message)}})
			return e
		}), done, nil
	case "twitter":
		cfg := oauth1.NewConfig(c["consumer_key"], c["consumer_secret"])
		tok := oauth1.NewToken(c["access_token"], c["access_token_secret"])
		client := cfg.Client(context.WithValue(ctx, oauth1.HTTPClient, hc), tok)
		client.Timeout = hc.Timeout
		client.CheckRedirect = hc.CheckRedirect
		sdk := twittersdk.NewClient(client)
		return sendFn(func(ctx context.Context, subject, message string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			_, _, e := sdk.DirectMessages.EventsNew(&twittersdk.DirectMessageEventsNewParams{Event: &twittersdk.DirectMessageEvent{
				Type: "message_create", Message: &twittersdk.DirectMessageEventMessage{
					Target: &twittersdk.DirectMessageTarget{RecipientID: target},
					Data:   &twittersdk.DirectMessageData{Text: oneBody(subject, message)},
				},
			}})
			return e
		}), done, nil
	case "viber":
		return newViberHTTPNotifier(hc, c, target), done, nil
	case "webpush":
		var sub webpush.Subscription
		if e := json.Unmarshal([]byte(target), &sub); e != nil {
			return nil, nil, e
		}
		s := webpush.New(c["vapid_public_key"], c["vapid_private_key"])
		s.AddReceivers(sub)
		ttl := 3600
		if c["ttl"] != "" {
			n, e := strconv.Atoi(c["ttl"])
			if e != nil {
				return nil, nil, e
			}
			ttl = n
		}
		return sendFn(func(ctx context.Context, subject, message string) error {
			opts := webpush.Options{Subscriber: c["subscriber"], TTL: ttl, HTTPClient: hc}
			return s.Send(webpush.WithOptions(ctx, opts), subject, message)
		}), done, nil
	case "wechat":
		return newWeChatHTTPNotifier(ctx, hc, c, target)
	case "whatsapp":
		return nil, nil, errors.New("notify WhatsApp adapter is a no-op and cannot deliver")
	default:
		return nil, nil, fmt.Errorf("unknown notification service %q", kind)
	}
}
