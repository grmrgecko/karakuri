package errors

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppErrorSerializes locks the wire shape of an error response. The
// frontend switches on the code string and renders the message, so both are
// part of the API rather than internals: renaming a code silently changes what
// every client sees. The StatusCode and wrapped cause carry the reason a
// request failed and must stay server-side.
func TestAppErrorSerializes(t *testing.T) {
	err := ErrValidation.
		WithMessage("invalid email").
		WithError(errors.New("db timeout")).
		WithDetails(map[string]string{"email": "must be a valid email address"})

	encoded, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr, "an AppError must always encode: it is the body of every failed request")

	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body), "the encoded error must be valid JSON")

	assert.Equal(t, "VALIDATION_ERROR", body["error"], "clients switch on the code string, so it is part of the API")
	assert.Equal(t, "invalid email", body["message"], "the caller-facing message must survive encoding")
	assert.Equal(t, map[string]any{"email": "must be a valid email address"}, body["details"],
		"per-field details must reach the form that has to display them")
	assert.NotContains(t, body, "StatusCode", "the status is carried by the HTTP response, not the body")
	assert.NotContains(t, body, "err", "the wrapped cause is for the log, never for the caller")
}

// TestAppErrorUnwraps holds AppError to the standard library's error contract.
// Handlers return a wrapped AppError and the router recovers it with errors.As
// to pick a status, while logging matches the cause with errors.Is; either one
// silently breaking would turn every domain error into a 500.
func TestAppErrorUnwraps(t *testing.T) {
	cause := errors.New("duplicate key")
	wrapped := ErrConflict.WithErrorMessage(cause)

	var recovered *AppError
	require.True(t, errors.As(wrapped, &recovered), "the router recovers the status through errors.As")
	assert.Equal(t, http.StatusConflict, recovered.HTTPStatus(), "the recovered error must still carry its status")
	assert.Equal(t, "duplicate key", recovered.Message, "WithErrorMessage promotes the cause to the caller-facing message")
	assert.ErrorIs(t, wrapped, cause, "the cause must stay reachable for the log")

	// Deriving must not mutate the shared sentinel: the package exports one
	// value per code and every handler derives from it, so a derivation that
	// wrote through would leak one request's message onto the next.
	assert.Equal(t, "Resource conflict", ErrConflict.Message,
		"deriving from a sentinel must leave the sentinel's own default message alone")
}
