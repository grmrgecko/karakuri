package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nikoksr/notify"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// responseJSON reads bounded provider responses without returning response bodies.
func responseJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider HTTP status %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// jsonRequest binds every owned provider request to execution cancellation.
func jsonRequest(ctx context.Context, method, address string, value any) (*http.Request, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(raw))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, err
}

// authenticatedGoogleClient keeps credentials inline and token requests on the private transport.
func authenticatedGoogleClient(ctx context.Context, client *http.Client, raw []byte, scope string) (*http.Client, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	credentials, err := google.CredentialsFromJSON(ctx, raw, scope)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &oauth2.Transport{Source: credentials.TokenSource, Base: client.Transport}, Timeout: client.Timeout, CheckRedirect: client.CheckRedirect}, nil
}

// newFirebaseNotifier checks the single-message response instead of discarding batch failures.
func newFirebaseNotifier(ctx context.Context, client *http.Client, c map[string]string, target string) (notify.Notifier, func(), error) {
	var credentials struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(c["credentials_json"]), &credentials); err != nil {
		return nil, nil, err
	}
	project := c["project_id"]
	if project == "" {
		project = credentials.ProjectID
	}
	if project == "" {
		return nil, nil, fmt.Errorf("firebase project is required")
	}
	auth, err := authenticatedGoogleClient(ctx, client, []byte(c["credentials_json"]), "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, nil, err
	}
	return sendFn(func(ctx context.Context, subject, message string) error {
		req, err := jsonRequest(ctx, http.MethodPost, "https://fcm.googleapis.com/v1/projects/"+url.PathEscape(project)+"/messages:send", map[string]any{"message": map[string]any{"token": target, "notification": map[string]string{"title": subject, "body": message}}})
		if err != nil {
			return err
		}
		var result struct {
			Name string `json:"name"`
		}
		if err = responseJSON(auth, req, &result); err != nil {
			return err
		}
		if result.Name == "" {
			return fmt.Errorf("firebase did not accept the message")
		}
		return nil
	}), func() {}, nil
}

// newMattermostHTTPNotifier owns token login and delivery instead of the global SDK client.
func newMattermostHTTPNotifier(ctx context.Context, client *http.Client, c map[string]string, target string) (notify.Notifier, func(), error) {
	token := c["token"]
	base := strings.TrimRight(c["server_url"], "/")
	if token == "" {
		req, err := jsonRequest(ctx, http.MethodPost, base+"/api/v4/users/login", map[string]string{"login_id": c["user_name"], "password": c["password"]})
		if err != nil {
			return nil, nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, nil, fmt.Errorf("mattermost login refused")
		}
		token = resp.Header.Get("Token")
		if token == "" {
			return nil, nil, fmt.Errorf("mattermost login returned no token")
		}
	}
	return sendFn(func(ctx context.Context, subject, message string) error {
		req, err := jsonRequest(ctx, http.MethodPost, base+"/api/v4/posts", map[string]string{"channel_id": target, "message": oneBody(subject, message)})
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		var result struct {
			ID string `json:"id"`
		}
		if err = responseJSON(client, req, &result); err != nil {
			return err
		}
		if result.ID == "" {
			return fmt.Errorf("mattermost did not accept the message")
		}
		return nil
	}), func() {}, nil
}

// newPushoverHTTPNotifier checks the API status as well as the HTTP status.
func newPushoverHTTPNotifier(client *http.Client, token, target string) notify.Notifier {
	return sendFn(func(ctx context.Context, subject, message string) error {
		values := url.Values{"token": {token}, "user": {target}, "title": {subject}, "message": {message}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", strings.NewReader(values.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		var result struct {
			Status int `json:"status"`
		}
		if err = responseJSON(client, req, &result); err != nil {
			return err
		}
		if result.Status != 1 {
			return fmt.Errorf("pushover refused the message")
		}
		return nil
	})
}

// newRocketChatHTTPNotifier avoids the SDK's global HTTP client.
func newRocketChatHTTPNotifier(client *http.Client, c map[string]string, target string) notify.Notifier {
	return sendFn(func(ctx context.Context, subject, message string) error {
		req, err := jsonRequest(ctx, http.MethodPost, c["scheme"]+"://"+c["server_host"]+"/api/v1/chat.postMessage", map[string]string{"channel": target, "text": oneBody(subject, message)})
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", c["token"])
		req.Header.Set("X-User-Id", c["user_id"])
		var result struct {
			Success bool `json:"success"`
		}
		if err = responseJSON(client, req, &result); err != nil {
			return err
		}
		if !result.Success {
			return fmt.Errorf("rocket.chat refused the message")
		}
		return nil
	})
}

// newViberHTTPNotifier preserves API rejection errors and request cancellation.
func newViberHTTPNotifier(client *http.Client, c map[string]string, target string) notify.Notifier {
	return sendFn(func(ctx context.Context, subject, message string) error {
		req, err := jsonRequest(ctx, http.MethodPost, "https://chatapi.viber.com/pa/send_message", map[string]any{"receiver": target, "sender": map[string]string{"name": c["sender_name"], "avatar": c["sender_avatar"]}, "type": "text", "text": oneBody(subject, message)})
		if err != nil {
			return err
		}
		req.Header.Set("X-Viber-Auth-Token", c["app_key"])
		var result struct {
			Status *int `json:"status"`
		}
		if err = responseJSON(client, req, &result); err != nil {
			return err
		}
		if result.Status == nil || *result.Status != 0 {
			return fmt.Errorf("viber refused the message")
		}
		return nil
	})
}

// newWeChatHTTPNotifier keeps token acquisition and delivery off global SDK clients.
func newWeChatHTTPNotifier(ctx context.Context, client *http.Client, c map[string]string, target string) (notify.Notifier, func(), error) {
	return sendFn(func(ctx context.Context, subject, message string) error {
		values := url.Values{"grant_type": {"client_credential"}, "appid": {c["app_id"]}, "secret": {c["app_secret"]}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/cgi-bin/token?"+values.Encode(), nil)
		if err != nil {
			return err
		}
		var token struct {
			AccessToken string `json:"access_token"`
			Error       int    `json:"errcode"`
		}
		if err = responseJSON(client, req, &token); err != nil {
			return err
		}
		if token.Error != 0 || token.AccessToken == "" {
			return fmt.Errorf("wechat authentication refused")
		}
		req, err = jsonRequest(ctx, http.MethodPost, "https://api.weixin.qq.com/cgi-bin/message/custom/send?"+url.Values{"access_token": {token.AccessToken}}.Encode(), map[string]any{"touser": target, "msgtype": "text", "text": map[string]string{"content": oneBody(subject, message)}})
		if err != nil {
			return err
		}
		var result struct {
			Error *int `json:"errcode"`
		}
		if err = responseJSON(client, req, &result); err != nil {
			return err
		}
		if result.Error == nil || *result.Error != 0 {
			return fmt.Errorf("wechat refused the message")
		}
		return nil
	}), func() {}, nil
}
