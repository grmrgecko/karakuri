package notification

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// Kind identifies notification settings in the shared connection store.
const Kind = "notification"

// Mask is the existing connection unchanged-secret sentinel.
const Mask = "••••••"

// Endpoint binds a stable author-facing ID to one private provider target.
type Endpoint struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Target string `json:"target,omitempty"`
}

// Config is encrypted with the application's existing connection key.
type Config struct {
	Service     string            `json:"service"`
	Credentials map[string]string `json:"credentials"`
	Endpoints   []Endpoint        `json:"endpoints"`
}

// decode accepts objects and the generic connection form's JSON text fields.
func decode(raw []byte) (Config, error) {
	var value struct {
		Service     string          `json:"service"`
		Credentials json.RawMessage `json:"credentials"`
		Endpoints   json.RawMessage `json:"endpoints"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return Config{}, fmt.Errorf("invalid notification configuration")
	}
	c := Config{Service: value.Service, Credentials: map[string]string{}}
	for _, field := range []struct {
		raw  json.RawMessage
		dest any
	}{{value.Credentials, &c.Credentials}, {value.Endpoints, &c.Endpoints}} {
		data := field.raw
		if len(data) == 0 {
			continue
		}
		if data[0] == '"' {
			var text string
			if json.Unmarshal(data, &text) != nil {
				return Config{}, fmt.Errorf("invalid notification settings JSON")
			}
			data = []byte(text)
		}
		if json.Unmarshal(data, field.dest) != nil {
			return Config{}, fmt.Errorf("invalid notification settings JSON")
		}
	}
	return c, nil
}

var endpointID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var dashboardRef = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var phone = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{2,30}$`)

// Validate checks every allowed target without constructing network clients.
func (c Config) Validate() error {
	s, ok := serviceFor(c.Service)
	if !ok {
		return fmt.Errorf("unknown notification service")
	}
	if s.Unavailable != "" {
		return fmt.Errorf("%s", s.Unavailable)
	}
	fields := map[string]flow.FieldSpec{}
	for _, f := range s.ConfigSpec {
		fields[f.ID] = f
		if f.Required && strings.TrimSpace(c.Credentials[f.ID]) == "" {
			return fmt.Errorf("notification settings require %s", f.ID)
		}
	}
	for k, v := range c.Credentials {
		if _, ok := fields[k]; !ok {
			return fmt.Errorf("unknown notification credential field %q", k)
		}
		if v == Mask {
			return fmt.Errorf("re-enter notification credential %q", k)
		}
		if len(v) > 1<<20 {
			return fmt.Errorf("notification credential %q exceeds 1 MiB", k)
		}
		if strings.HasSuffix(k, "_json") && v != "" {
			var data map[string]any
			if json.Unmarshal([]byte(v), &data) != nil || data == nil {
				return fmt.Errorf("notification credential %q must be a JSON object", k)
			}
		}
	}
	if len(c.Endpoints) < 1 || len(c.Endpoints) > 100 {
		return fmt.Errorf("notification settings require 1–100 allowed endpoints")
	}
	seen := map[string]bool{}
	for _, e := range c.Endpoints {
		if !endpointID.MatchString(e.ID) || seen[e.ID] {
			return fmt.Errorf("notification endpoint IDs must be unique and use 1–64 letters, digits, underscores or hyphens")
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.Label) == "" || len(e.Label) > 120 {
			return fmt.Errorf("notification endpoint labels require 1–120 bytes")
		}
		if strings.TrimSpace(e.Target) == "" || len(e.Target) > 32768 || e.Target == Mask || strings.ContainsAny(e.Target, "\r\n\x00") {
			return fmt.Errorf("notification endpoint %q requires a target of 1–32768 bytes", e.ID)
		}
		if err := validateTarget(c.Service, s.TargetFormat, e.Target); err != nil {
			return fmt.Errorf("notification endpoint %q: %s", e.ID, err)
		}
	}
	return validateCredentials(c)
}

// validateTarget rejects malformed recipients without echoing private targets.
func validateTarget(service, format, target string) error {
	switch {
	case strings.Contains(format, "http(s)"):
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("target must be an http(s) URL without user information")
		}
	case format == "email":
		if _, err := mail.ParseAddress(target); err != nil || strings.ContainsAny(target, ",;") {
			return fmt.Errorf("target must be one email address")
		}
	case format == "phone number":
		if !phone.MatchString(target) {
			return fmt.Errorf("target must be one phone number")
		}
	case service == "dashboard":
		if !dashboardRef.MatchString(target) {
			return fmt.Errorf("target must be a dashboard ID or slug")
		}
	case service == "telegram":
		n, err := strconv.ParseInt(target, 10, 64)
		if err != nil || n == 0 {
			return fmt.Errorf("target must be a nonzero signed chat ID")
		}
	case service == "twitter":
		n, err := strconv.ParseUint(target, 10, 64)
		if err != nil || n == 0 {
			return fmt.Errorf("target must be a numeric user ID")
		}
	case service == "amazonsns":
		if !strings.HasPrefix(target, "arn:") || !strings.Contains(target, ":sns:") {
			return fmt.Errorf("target must be an SNS topic ARN")
		}
	case service == "googlechat":
		if strings.ContainsAny(target, "/ ") {
			return fmt.Errorf("target must be a bare space ID")
		}
	case service == "webpush":
		var sub struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		}
		if json.Unmarshal([]byte(target), &sub) != nil || sub.Keys.P256dh == "" || sub.Keys.Auth == "" || validateTarget("http", "http(s)", sub.Endpoint) != nil {
			return fmt.Errorf("target must be a web push subscription with endpoint and keys")
		}
	case service == "lark":
		var r struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(target), &r) != nil || r.ID == "" || (r.Type != "chat_id" && r.Type != "open_id" && r.Type != "user_id" && r.Type != "union_id" && r.Type != "email") {
			return fmt.Errorf("target must contain id and type (chat_id, open_id, user_id, union_id or email)")
		}
	case service == "syslog":
		var r struct {
			Network string `json:"network"`
			Address string `json:"address"`
		}
		if json.Unmarshal([]byte(target), &r) != nil || r.Address == "" || (r.Network != "udp" && r.Network != "tcp" && r.Network != "unix" && r.Network != "unixgram") {
			return fmt.Errorf("target must contain a syslog network and address")
		}
	}
	return nil
}

// validateCredentials checks settings used as protocol addresses or options.
func validateCredentials(c Config) error {
	for _, k := range []string{"server_url", "homeserver"} {
		if v := c.Credentials[k]; v != "" && validateTarget("http", "http(s)", v) != nil {
			return fmt.Errorf("notification %s must be an http(s) URL", k)
		}
	}
	for _, k := range []string{"sender_address", "from_address"} {
		if v := c.Credentials[k]; v != "" && (strings.ContainsAny(v, "\r\n\x00") || validateTarget("mail", "email", v) != nil) {
			return fmt.Errorf("notification %s must be one email address", k)
		}
	}
	if c.Service == "mail" {
		host, port, err := net.SplitHostPort(c.Credentials["smtp_host"])
		n, numberErr := strconv.Atoi(port)
		if err != nil || numberErr != nil || host == "" || n < 1 || n > 65535 {
			return fmt.Errorf("notification smtp_host requires host:port")
		}
	}
	for key, allowed := range map[string][]string{"auth_type": {"bot", "oauth2"}, "body_format": {"text", "html"}, "mode": {"transactional", "bulk", "sandbox"}, "wrap_text": {"true", "false"}, "urgency": {"high", "low"}, "notification_type": {"incident"}, "scheme": {"http", "https"}} {
		if value := c.Credentials[key]; value != "" {
			valid := false
			for _, option := range allowed {
				if option == value {
					valid = true
				}
			}
			if !valid {
				return fmt.Errorf("notification %s must be %s", key, strings.Join(allowed, " or "))
			}
		}
	}
	if c.Service == "dashboard" {
		if v := c.Credentials["style"]; v != "" && v != "toast" && v != "fullscreen" {
			return fmt.Errorf("notification dashboard style must be toast or fullscreen")
		}
		if v := c.Credentials["level"]; v != "" && v != "info" && v != "success" && v != "warning" && v != "error" {
			return fmt.Errorf("notification dashboard level must be info, success, warning or error")
		}
		if v := c.Credentials["speak"]; v != "" && v != "true" && v != "false" {
			return fmt.Errorf("notification dashboard speak must be true or false")
		}
		if v := c.Credentials["duration"]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 86400 {
				return fmt.Errorf("notification dashboard duration must be an integer from 0 to 86400")
			}
		}
	}
	if c.Service == "mailgun" && c.Credentials["region"] != "" && c.Credentials["region"] != "us" && c.Credentials["region"] != "eu" {
		return fmt.Errorf("notification Mailgun region must be us or eu")
	}
	if c.Service == "mailtrap" && c.Credentials["mode"] == "sandbox" && c.Credentials["inbox_id"] == "" {
		return fmt.Errorf("notification Mailtrap sandbox requires inbox_id")
	}
	if c.Service == "mattermost" && c.Credentials["token"] == "" && (c.Credentials["user_name"] == "" || c.Credentials["password"] == "") {
		return fmt.Errorf("notification Mattermost requires a token or user name and password")
	}
	for key, max := range map[string]int{"priority": 191, "ttl": 2419200} {
		if value := c.Credentials[key]; value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > max {
				return fmt.Errorf("notification %s must be an integer from 0 to %d", key, max)
			}
		}
	}
	if strings.ContainsAny(c.Credentials["tag"], " \r\n\x00") {
		return fmt.Errorf("notification syslog tag must not contain spaces, line breaks or NUL")
	}
	if c.Service == "fcm" || c.Service == "googlechat" {
		var account struct {
			Type    string `json:"type"`
			Email   string `json:"client_email"`
			Key     string `json:"private_key"`
			Project string `json:"project_id"`
		}
		if json.Unmarshal([]byte(c.Credentials["credentials_json"]), &account) != nil || account.Type != "service_account" || account.Email == "" || account.Key == "" {
			return fmt.Errorf("notification Google credentials require inline service_account JSON with client_email and private_key")
		}
		if c.Service == "fcm" && account.Project == "" && c.Credentials["project_id"] == "" {
			return fmt.Errorf("notification Firebase credentials require project_id")
		}
	}
	if c.Service == "rocketchat" {
		u, err := url.Parse(c.Credentials["scheme"] + "://" + c.Credentials["server_host"])
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("notification Rocket.Chat server_host requires a host with optional port")
		}
	}

	if c.Service == "http" {
		method := c.Credentials["method"]
		if method != "" && method != "POST" && method != "PUT" {
			return fmt.Errorf("notification HTTP method must be POST or PUT")
		}
		var headers map[string]string
		if v := c.Credentials["headers_json"]; v != "" && json.Unmarshal([]byte(v), &headers) != nil {
			return fmt.Errorf("notification HTTP headers must map names to string values")
		}
		for key, value := range headers {
			if key == "" || strings.ContainsAny(key, " ()<>@,;:\"/[]?={}\t\r\n\x00") || strings.ContainsAny(value, "\r\n\x00") {
				return fmt.Errorf("notification HTTP headers contain an invalid name or value")
			}
		}
	}
	return nil
}

// configMap normalizes structured values before returning or restoring secrets.
func configMap(c Config, cfg map[string]any) {
	raw, _ := json.Marshal(c)
	var normalized map[string]any
	_ = json.Unmarshal(raw, &normalized)
	for k := range cfg {
		delete(cfg, k)
	}
	for k, v := range normalized {
		cfg[k] = v
	}
}

// MaskConfig redacts credentials and every provider target while retaining IDs.
func MaskConfig(cfg map[string]any) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	c, err := decode(raw)
	if err != nil {
		return err
	}
	s, ok := serviceFor(c.Service)
	if !ok {
		return fmt.Errorf("unknown notification service")
	}
	for _, f := range s.ConfigSpec {
		if f.Control == "password" && c.Credentials[f.ID] != "" {
			c.Credentials[f.ID] = Mask
		}
	}
	// HTTP headers can contain authentication, even when entered as ordinary JSON.
	if c.Credentials["headers_json"] != "" {
		c.Credentials["headers_json"] = Mask
	}
	for i := range c.Endpoints {
		if c.Endpoints[i].Target != "" {
			c.Endpoints[i].Target = Mask
		}
	}
	configMap(c, cfg)
	return nil
}

// RestoreConfig binds unchanged target masks to IDs, never row positions.
func RestoreConfig(cfg, stored map[string]any) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	c, err := decode(raw)
	if err != nil {
		return err
	}
	var old Config
	if stored != nil {
		raw, err = json.Marshal(stored)
		if err != nil {
			return err
		}
		old, err = decode(raw)
		if err != nil {
			return err
		}
		if old.Service != c.Service {
			return fmt.Errorf("notification service type is fixed; create a new service")
		}
	}
	for k, v := range c.Credentials {
		if v == Mask {
			value := old.Credentials[k]
			if value == "" {
				return fmt.Errorf("re-enter notification credential %q", k)
			}
			c.Credentials[k] = value
		}
	}
	targets := map[string]string{}
	for _, e := range old.Endpoints {
		targets[e.ID] = e.Target
	}
	for i, e := range c.Endpoints {
		if e.Target == Mask {
			value := targets[e.ID]
			if value == "" {
				return fmt.Errorf("re-enter target for notification endpoint %q", e.ID)
			}
			c.Endpoints[i].Target = value
		}
	}
	configMap(c, cfg)
	return nil
}

// KindDef reuses encrypted connection settings and their permission checks.
func KindDef() integrations.ConnectionKind {
	options := []flow.Option{}
	for _, s := range Services() {
		options = append(options, flow.Option{Value: s.ID, Label: s.Label})
	}
	return integrations.ConnectionKind{Kind: Kind, Label: "Notification service", Factory: New, MaskConfig: MaskConfig, RestoreConfig: RestoreConfig,
		ConfigSpec: []flow.FieldSpec{{ID: "service", Label: "Service", Type: flow.FieldSelect, Required: true, Options: options}, {ID: "credentials", Label: "Credentials", Type: flow.FieldJSON, Help: "Service-specific settings. Settings → Notifications provides named fields."}, {ID: "endpoints", Label: "Allowed endpoints", Type: flow.FieldJSON, Required: true, Help: "Array of {id, label, target}. Settings → Notifications provides endpoint rows."}},
		Validate: func(raw []byte) error {
			c, err := decode(raw)
			if err != nil {
				return err
			}
			return c.Validate()
		}}
}

// EndpointList reads only the public allowlist identities from stored settings.
func EndpointList(raw []byte) ([]Endpoint, error) {
	c, err := decode(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Endpoint, 0, len(c.Endpoints))
	for _, e := range c.Endpoints {
		out = append(out, Endpoint{ID: e.ID, Label: e.Label})
	}
	return out, nil
}
