package tsdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Error classes. They decide retries in nodes and writers and the REST status.
const (
	// ClassUnavailable is a source that could not be reached or answered
	// with a server-side failure; retrying later may succeed.
	ClassUnavailable = "unavailable"
	// ClassRejected is a query or batch the source refused as malformed;
	// retrying unchanged will fail the same way.
	ClassRejected = "rejected"
	// ClassUnsupported is an operation the connection cannot perform.
	ClassUnsupported = "unsupported"
)

// Error is a classified failure from a time-series source.
type Error struct {
	Class   string
	Message string
	Err     error
	// Status is the HTTP status the source answered with, or 0.
	Status int
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Class
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable reports whether the failure may clear on its own.
func (e *Error) Retryable() bool { return e.Class == ClassUnavailable }

// Unavailable wraps err as a retryable failure.
func Unavailable(err error) *Error {
	return &Error{Class: ClassUnavailable, Message: Redact(err.Error()), Err: err}
}

// Unavailablef builds a retryable failure from a message.
func Unavailablef(format string, args ...any) *Error {
	return &Error{Class: ClassUnavailable, Message: Redact(fmt.Sprintf(format, args...))}
}

// Rejectedf builds a failure the source will repeat.
func Rejectedf(format string, args ...any) *Error {
	return &Error{Class: ClassRejected, Message: Redact(fmt.Sprintf(format, args...))}
}

// Unsupportedf builds a failure for an operation the connection lacks.
func Unsupportedf(format string, args ...any) *Error {
	return &Error{Class: ClassUnsupported, Message: fmt.Sprintf(format, args...)}
}

// Classify returns err as *Error. Context deadlines and transport failures
// are unavailable; anything already classified passes through; the rest is
// treated as unavailable, the cautious choice for a writer that would
// otherwise discard data.
func Classify(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Class: ClassUnavailable, Message: "timed out", Err: err}
	}
	return Unavailable(err)
}

// ClassifyStatus classifies an HTTP failure status. 408, 429, and 5xx may
// clear; other 4xx statuses are the request's fault.
func ClassifyStatus(status int, body string) *Error {
	msg := strings.TrimSpace(body)
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	} else {
		msg = fmt.Sprintf("HTTP %d: %s", status, msg)
	}
	if status == 408 || status == 429 || status >= 500 {
		return &Error{Class: ClassUnavailable, Message: Redact(msg), Status: status}
	}
	return &Error{Class: ClassRejected, Message: Redact(msg), Status: status}
}

// WriteError reclassifies an HTTP write failure for a sink. A 401, 403, or
// 404 on a write is a revoked token, a deleted bucket or database, or a
// disabled receiver: the destination's state, which an operator fixes. The
// batch is good and stays buffered, so those are unavailable rather than
// rejected. Queries keep ClassifyStatus's reading, where the same statuses
// are the request's fault.
func WriteError(err error) error {
	var e *Error
	if !errors.As(err, &e) {
		return err
	}
	switch e.Status {
	case 401, 403, 404:
		return &Error{Class: ClassUnavailable, Message: e.Message, Err: e.Err, Status: e.Status}
	}
	return err
}

// Redact removes user information from URLs in a message, since net/http
// errors quote the request URL and a URL may carry a password.
func Redact(msg string) string {
	fields := strings.Fields(msg)
	changed := false
	for i, f := range fields {
		trimmed := strings.Trim(f, `"'`)
		if !strings.Contains(trimmed, "://") || !strings.Contains(trimmed, "@") {
			continue
		}
		u, err := url.Parse(trimmed)
		if err != nil || u.User == nil {
			continue
		}
		u.User = nil
		fields[i] = strings.Replace(f, trimmed, u.String(), 1)
		changed = true
	}
	if !changed {
		return msg
	}
	return strings.Join(fields, " ")
}
