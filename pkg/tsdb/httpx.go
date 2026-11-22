package tsdb

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxResponseBytes bounds a database response a driver reads into memory.
const MaxResponseBytes = 64 << 20

// DefaultTimeout is a connection's query timeout when it names none.
const DefaultTimeout = 30 * time.Second

// NewHTTPClient builds the client a driver talks to its database with.
func NewHTTPClient(skipVerify bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if skipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator's explicit choice
	}
	return &http.Client{Transport: tr}
}

// Do sends req and returns the body of a 2xx response. Other statuses become
// classified errors carrying the response body; transport failures are
// unavailable.
func Do(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		if req.Context().Err() == context.DeadlineExceeded {
			return nil, &Error{Class: ClassUnavailable, Message: "timed out", Err: err}
		}
		return nil, Unavailable(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, Unavailable(err)
	}
	if len(body) > MaxResponseBytes {
		return nil, Rejectedf("response is larger than %d MiB", MaxResponseBytes>>20)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, ClassifyStatus(resp.StatusCode, errorText(body))
	}
	return body, nil
}

// errorText pulls the message out of the JSON error bodies databases send,
// falling back to the raw body.
func errorText(body []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Error != "" {
			return e.Error
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return string(body)
}

// JoinURL appends a path to a base URL without doubling slashes.
func JoinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// Seconds turns a configured seconds value into a duration with a default.
func Seconds(v float64, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return time.Duration(v * float64(time.Second))
}

// MiB turns a configured buffer size into bytes, 64 MiB by default.
func MiB(v float64) int64 {
	if v <= 0 {
		return 64 << 20
	}
	return int64(v * (1 << 20))
}

// RequireURL checks a configured base URL.
func RequireURL(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return fmt.Errorf("%s must start with http:// or https://", field)
	}
	return nil
}
