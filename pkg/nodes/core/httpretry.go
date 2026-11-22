package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxErrorBodyBytes = 64 << 10

func (c *httpRequestConfig) validateRetry() error {
	switch c.RetryMode {
	case "", "safe", "all", "none":
	default:
		return fmt.Errorf("unknown retry methods %q", c.RetryMode)
	}
	statuses := strings.TrimSpace(c.RetryStatuses)
	if statuses == "" {
		statuses = "408,429,500,502,503,504"
	}
	c.retryCodes = map[int]bool{}
	for _, token := range strings.Split(statuses, ",") {
		code, err := strconv.Atoi(strings.TrimSpace(token))
		if err != nil || code < 400 || code > 599 {
			return fmt.Errorf("retry statuses must be comma-separated codes between 400 and 599")
		}
		c.retryCodes[code] = true
	}
	return nil
}

func (c httpRequestConfig) mayRetry(method string) bool {
	switch c.RetryMode {
	case "none":
		return false
	case "all":
		return true
	default:
		return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	}
}

// transientHTTPError excludes TLS verification failures from transport retries.
func transientHTTPError(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &verification) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return false
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// failureURL excludes query credentials and URL userinfo from failure history.
func failureURL(u *url.URL) string {
	safe := *u
	safe.User = nil
	safe.RawQuery = ""
	safe.ForceQuery = false
	safe.Fragment = ""
	return safe.String()
}

func safeResponseHeaders(headers http.Header) map[string]string {
	safe := map[string]string{}
	for _, name := range []string{"Content-Type", "Content-Length", "Retry-After", "X-Request-Id", "X-Correlation-Id", "RateLimit", "RateLimit-Remaining", "RateLimit-Reset"} {
		if value := headers.Get(name); value != "" {
			if len(value) > 1024 {
				value = value[:1024]
			}
			safe[name] = value
		}
	}
	return safe
}

func boundedErrorBody(raw []byte) (string, bool) {
	if len(raw) > maxErrorBodyBytes {
		return string(raw[:maxErrorBodyBytes]), true
	}
	return string(raw), false
}

// parseRetryAfter accepts RFC 9110 unsigned delay seconds or an HTTP date.
// Invalid or past values fall back to the configured backoff.
func parseRetryAfter(raw string, now time.Time) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	digits := true
	for _, r := range raw {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || seconds > math.MaxInt64/1000 {
			return 0
		}
		return int64(seconds) * 1000
	}
	date, err := http.ParseTime(raw)
	if err != nil || !date.After(now) {
		return 0
	}
	delay := date.Sub(now)
	ms := delay.Milliseconds()
	if delay%time.Millisecond != 0 {
		ms++
	}
	return ms
}
