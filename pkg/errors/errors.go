package errors

import (
	"fmt"
	"net/http"
)

// AppError represents a structured application error with an HTTP status code.
type AppError struct {
	Code       string            `json:"error"`
	Message    string            `json:"message"`
	Details    map[string]string `json:"details,omitempty"`
	StatusCode int               `json:"-"`
	Err        error             `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// HTTPStatus returns the HTTP status code for integration with HTTP error handling.
func (e *AppError) HTTPStatus() int {
	return e.StatusCode
}

// WithError returns a copy with the given underlying error attached.
func (e *AppError) WithError(err error) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    e.Details,
		StatusCode: e.StatusCode,
		Err:        err,
	}
}

// WithMessage returns a copy with a replacement message.
func (e *AppError) WithMessage(msg string) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    msg,
		Details:    e.Details,
		StatusCode: e.StatusCode,
		Err:        e.Err,
	}
}

// WithDetails returns a copy with additional detail fields for validation errors.
func (e *AppError) WithDetails(details map[string]string) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    details,
		StatusCode: e.StatusCode,
		Err:        e.Err,
	}
}

// WithErrorMessage returns a copy with the underlying error set and its message
// used as the user-facing message.
func (e *AppError) WithErrorMessage(err error) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    err.Error(),
		Details:    e.Details,
		StatusCode: e.StatusCode,
		Err:        err,
	}
}

// Sentinel error values used throughout the application.
var (
	ErrNotFound           = &AppError{Code: "NOT_FOUND", Message: "Resource not found", StatusCode: http.StatusNotFound}
	ErrUnauthorized       = &AppError{Code: "UNAUTHORIZED", Message: "Unauthorized", StatusCode: http.StatusUnauthorized}
	ErrForbidden          = &AppError{Code: "FORBIDDEN", Message: "Forbidden", StatusCode: http.StatusForbidden}
	ErrValidation         = &AppError{Code: "VALIDATION_ERROR", Message: "Validation error", StatusCode: http.StatusBadRequest}
	ErrInternal           = &AppError{Code: "INTERNAL_ERROR", Message: "Internal server error", StatusCode: http.StatusInternalServerError}
	ErrDatabaseError      = &AppError{Code: "DATABASE_ERROR", Message: "Database error", StatusCode: http.StatusInternalServerError}
	ErrConflict           = &AppError{Code: "CONFLICT", Message: "Resource conflict", StatusCode: http.StatusConflict}
	// ErrAmbiguous answers a slug several items the caller can see share; its
	// details list them by ID so the caller can choose one.
	ErrAmbiguous = &AppError{Code: "AMBIGUOUS", Message: "Several items use this address", StatusCode: http.StatusConflict}
	ErrRateLimit          = &AppError{Code: "RATE_LIMITED", Message: "Too many requests", StatusCode: http.StatusTooManyRequests}
	ErrServiceUnavailable = &AppError{Code: "SERVICE_UNAVAILABLE", Message: "Service unavailable", StatusCode: http.StatusServiceUnavailable}
	ErrBadGateway         = &AppError{Code: "BAD_GATEWAY", Message: "Bad gateway", StatusCode: http.StatusBadGateway}
	ErrUnprocessable      = &AppError{Code: "UNPROCESSABLE", Message: "Request could not be processed", StatusCode: http.StatusUnprocessableEntity}
)
