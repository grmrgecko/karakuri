// Package notification sends to endpoints approved in encrypted settings.
package notification

import "github.com/grmrgecko/karakuri/pkg/flow"

// Service describes one upstream provider and its settings fields.
type Service struct {
	ID            string           `json:"id"`
	Label         string           `json:"label"`
	ConfigSpec    []flow.FieldSpec `json:"config_spec"`
	TargetFormat  string           `json:"target_format"`
	TargetExample string           `json:"target_example"`
	Unavailable   string           `json:"unavailable,omitempty"`
}

// services covers the 33 directories and 36 variants in notify v1.6.0, plus
// Karakuri's own dashboard alerts.
var services = []Service{
	{ID: "dashboard", Label: "Karakuri dashboard", TargetFormat: "dashboard ID or slug", TargetExample: "hallway-panel", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "style", Label: "Style", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "level", Label: "Level", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "duration", Label: "Duration (seconds)", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "speak", Label: "Read aloud", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "amazonses", Label: "Amazon SES", TargetFormat: "email", TargetExample: "alerts@example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "access_key_id", Label: "Access key ID", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "secret_key", Label: "Secret key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "region", Label: "Region", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "sender_address", Label: "Sender address", Type: flow.FieldString, Required: true, Control: ""},
	}},
	{ID: "amazonsns", Label: "Amazon SNS", TargetFormat: "topic ARN", TargetExample: "arn:aws:sns:us-east-1:123456789012:alerts", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "access_key_id", Label: "Access key ID", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "secret_key", Label: "Secret key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "region", Label: "Region", Type: flow.FieldString, Required: true, Control: ""},
	}},
	{ID: "bark", Label: "Bark", TargetFormat: "http(s) server URL", TargetExample: "https://api.day.app/", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "device_key", Label: "Device key", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "dingding", Label: "DingTalk", TargetFormat: "robot access token", TargetExample: "robot-token", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "secret", Label: "Secret", Type: flow.FieldString, Required: false, Control: "password"},
	}},
	{ID: "discord", Label: "Discord", TargetFormat: "channel ID", TargetExample: "123456789012345678", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "token", Label: "Token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "auth_type", Label: "Auth type", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "fcm", Label: "Firebase Cloud Messaging", TargetFormat: "device registration token", TargetExample: "fcm-registration-token", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "credentials_json", Label: "Credentials JSON", Type: flow.FieldText, Required: true, Control: "password"},
		{ID: "project_id", Label: "Project ID", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "googlechat", Label: "Google Chat", TargetFormat: "bare space ID", TargetExample: "AAAA12345", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "credentials_json", Label: "Credentials JSON", Type: flow.FieldText, Required: true, Control: "password"},
	}},
	{ID: "http", Label: "HTTP webhook", TargetFormat: "http(s) webhook URL", TargetExample: "https://example.com/notify", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "headers_json", Label: "Headers JSON", Type: flow.FieldText, Required: false, Control: "password"},
		{ID: "method", Label: "Method", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "lark", Label: "Lark custom app", TargetFormat: "JSON receiver descriptor", TargetExample: "{\"type\":\"chat_id\",\"id\":\"oc_xxx\"}", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "app_id", Label: "App ID", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "app_secret", Label: "App secret", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "lark-webhook", Label: "Lark webhook", TargetFormat: "http(s) webhook URL", TargetExample: "https://open.larksuite.com/open-apis/bot/v2/hook/token", Unavailable: "", ConfigSpec: []flow.FieldSpec{}},
	{ID: "line", Label: "LINE Messaging API", TargetFormat: "user, group or room ID", TargetExample: "U12345", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "channel_secret", Label: "Channel secret", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "channel_access_token", Label: "Channel access token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "line-notify", Label: "LINE Notify", TargetFormat: "access token", TargetExample: "line-notify-token", Unavailable: "LINE Notify was retired on March 31, 2025. Use the LINE bot service.", ConfigSpec: []flow.FieldSpec{}},
	{ID: "mail", Label: "SMTP mail", TargetFormat: "email", TargetExample: "alerts@example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "sender_address", Label: "Sender address", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "smtp_host", Label: "SMTP host", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "identity", Label: "Identity", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "user_name", Label: "User name", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "password", Label: "Password", Type: flow.FieldString, Required: false, Control: "password"},
		{ID: "auth_host", Label: "Auth host", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "body_format", Label: "Body format", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "mailgun", Label: "Mailgun", TargetFormat: "email", TargetExample: "alerts@example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "domain", Label: "Domain", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "api_key", Label: "API key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "sender_address", Label: "Sender address", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "region", Label: "Region", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "mailtrap", Label: "Mailtrap", TargetFormat: "email", TargetExample: "alerts@example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_key", Label: "API key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "sender_address", Label: "Sender address", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "sender_name", Label: "Sender name", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "mode", Label: "Mode", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "inbox_id", Label: "Inbox ID", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "body_format", Label: "Body format", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "matrix", Label: "Matrix", TargetFormat: "room ID", TargetExample: "!room:example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "user_id", Label: "User ID", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "homeserver", Label: "Homeserver", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "access_token", Label: "Access token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "mattermost", Label: "Mattermost", TargetFormat: "channel ID", TargetExample: "channel-id", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "server_url", Label: "Server URL", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "token", Label: "Token", Type: flow.FieldString, Required: false, Control: "password"},
		{ID: "user_name", Label: "User name", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "password", Label: "Password", Type: flow.FieldString, Required: false, Control: "password"},
	}},
	{ID: "msteams", Label: "Microsoft Teams", TargetFormat: "http(s) channel webhook URL", TargetExample: "https://example.webhook.office.com/xxx", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "wrap_text", Label: "Wrap text", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "pagerduty", Label: "PagerDuty", TargetFormat: "PagerDuty service ID", TargetExample: "P12345", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "token", Label: "Token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "from_address", Label: "From address", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "urgency", Label: "Urgency", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "priority_id", Label: "Priority ID", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "notification_type", Label: "Notification type", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "plivo", Label: "Plivo SMS", TargetFormat: "phone number", TargetExample: "+15551234567", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "auth_id", Label: "Auth ID", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "auth_token", Label: "Auth token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "source", Label: "Source", Type: flow.FieldString, Required: true, Control: ""},
	}},
	{ID: "pushbullet", Label: "Pushbullet", TargetFormat: "device nickname", TargetExample: "Phone", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_token", Label: "Api token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "pushbullet-sms", Label: "Pushbullet SMS", TargetFormat: "phone number", TargetExample: "+15551234567", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_token", Label: "Api token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "device_nickname", Label: "Device nickname", Type: flow.FieldString, Required: true, Control: ""},
	}},
	{ID: "pushover", Label: "Pushover", TargetFormat: "user/group key", TargetExample: "recipient-key", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "app_token", Label: "App token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "reddit", Label: "Reddit", TargetFormat: "username", TargetExample: "recipient_name", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "client_id", Label: "Client ID", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "client_secret", Label: "Client secret", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "user_name", Label: "User name", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "password", Label: "Password", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "rocketchat", Label: "Rocket.Chat", TargetFormat: "channel name", TargetExample: "general", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "server_host", Label: "Server host", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "scheme", Label: "Scheme", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "user_id", Label: "User ID", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "token", Label: "Token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "sendgrid", Label: "SendGrid", TargetFormat: "email", TargetExample: "alerts@example.com", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_key", Label: "API key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "sender_address", Label: "Sender address", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "sender_name", Label: "Sender name", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "body_format", Label: "Body format", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "slack", Label: "Slack", TargetFormat: "channel ID", TargetExample: "C12345", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_token", Label: "Api token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "syslog", Label: "Syslog", TargetFormat: "JSON dial endpoint", TargetExample: "{\"network\":\"udp\",\"address\":\"localhost:514\"}", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "priority", Label: "Priority", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "tag", Label: "Tag", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "telegram", Label: "Telegram", TargetFormat: "signed integer chat ID", TargetExample: "-1001234567890", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "api_token", Label: "Api token", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "textmagic", Label: "TextMagic", TargetFormat: "phone number", TargetExample: "+15551234567", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "user_name", Label: "User name", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "api_key", Label: "API key", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "twilio", Label: "Twilio SMS", TargetFormat: "phone number", TargetExample: "+15551234567", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "account_sid", Label: "Account SID", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "auth_token", Label: "Auth token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "from_phone_number", Label: "From phone number", Type: flow.FieldString, Required: true, Control: ""},
	}},
	{ID: "twitter", Label: "Twitter / X", TargetFormat: "numeric user ID", TargetExample: "12345678", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "consumer_key", Label: "Consumer key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "consumer_secret", Label: "Consumer secret", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "access_token", Label: "Access token", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "access_token_secret", Label: "Access token secret", Type: flow.FieldString, Required: true, Control: "password"},
	}},
	{ID: "viber", Label: "Viber", TargetFormat: "subscribed user ID", TargetExample: "viber-user-id", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "app_key", Label: "App key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "sender_name", Label: "Sender name", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "sender_avatar", Label: "Sender avatar", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "webpush", Label: "Web Push", TargetFormat: "JSON subscription", TargetExample: "{\"endpoint\":\"https://push.example.com/subscription\",\"keys\":{\"p256dh\":\"base64url\",\"auth\":\"base64url\"}}", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "vapid_public_key", Label: "VAPID public key", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "vapid_private_key", Label: "VAPID private key", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "subscriber", Label: "Subscriber", Type: flow.FieldString, Required: false, Control: ""},
		{ID: "ttl", Label: "TTL", Type: flow.FieldString, Required: false, Control: ""},
	}},
	{ID: "wechat", Label: "WeChat", TargetFormat: "OpenID", TargetExample: "wechat-openid", Unavailable: "", ConfigSpec: []flow.FieldSpec{
		{ID: "app_id", Label: "App ID", Type: flow.FieldString, Required: true, Control: ""},
		{ID: "app_secret", Label: "App secret", Type: flow.FieldString, Required: true, Control: "password"},
		{ID: "token", Label: "Token", Type: flow.FieldString, Required: false, Control: "password"},
		{ID: "encoding_aes_key", Label: "Encoding AES key", Type: flow.FieldString, Required: false, Control: "password"},
	}},
	{ID: "whatsapp", Label: "WhatsApp", TargetFormat: "contact", TargetExample: "15551234567", Unavailable: "The upstream WhatsApp adapter is a no-op and cannot deliver notifications.", ConfigSpec: []flow.FieldSpec{}},
}

// Services returns the stable settings catalog without credentials.
func Services() []Service {
	out := append([]Service(nil), services...)
	for i := range out {
		out[i].ConfigSpec = append([]flow.FieldSpec(nil), out[i].ConfigSpec...)
		for j := range out[i].ConfigSpec {
			f := &out[i].ConfigSpec[j]
			f.Help = credentialHelp[out[i].ID+"."+f.ID]
			if f.Help == "" {
				f.Help = credentialHelp[f.ID]
			}
		}
	}
	return out
}

// serviceFor resolves a provider before any sender is constructed.
func serviceFor(id string) (Service, bool) {
	for _, s := range services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

var credentialHelp = map[string]string{
	"method": "POST (default) or PUT.", "auth_type": "bot (default) or oauth2.", "body_format": "text or html. SMTP defaults to text; Mailtrap and SendGrid default to html.",
	"mode": "transactional (default), bulk or sandbox.", "mailgun.region": "us (default) or eu.", "wrap_text": "true (default) or false.", "urgency": "high (default) or low.", "notification_type": "incident (default).", "scheme": "http or https.",
	"dashboard.style": "toast (default) or fullscreen.", "dashboard.level": "info (default), success, warning or error.", "dashboard.duration": "Seconds from 0 to 86400; 0 keeps the alert until cleared; defaults to 10.", "dashboard.speak": "true to have open viewers read the alert aloud; defaults to false.",
	"smtp_host": "Host and port, for example smtp.example.com:587. STARTTLS is used when offered.", "auth_host": "Authentication host; defaults to the SMTP host.", "headers_json": "JSON object mapping header names to values. Values are masked after saving.", "credentials_json": "Inline Google service account JSON; credential files and ambient credentials are not used.", "ttl": "Seconds from 0 to 2419200; defaults to 3600.", "priority": "Syslog facility plus severity, from 0 to 191; defaults to 13.", "tag": "Syslog tag without spaces; defaults to karakuri.",
}
