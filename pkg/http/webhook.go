package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// maxWebhookBody bounds how much of a webhook request body is read.
const maxWebhookBody = 1 << 20

// mountWebhooks registers the webhook route on a router for every method. A
// trigger's own method list decides which ones fire it, so a route narrower
// than that list would answer for the trigger before it was asked.
func mountWebhooks(r chi.Router, reg *engine.WebhookRegistry) {
	r.Handle("/hook/{token}", webhookHandler(reg))
}

// webhookHandler resolves /hook/{token} requests against a webhook registry:
// 404 for unarmed tokens, 405 for methods the trigger does not accept, and
// either an immediate 202 or the flow's own response depending on the
// trigger's response mode.
func webhookHandler(reg *engine.WebhookRegistry) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		token := chi.URLParam(r, "token")
		registration, found, allowed := reg.Resolve(token, r.Method)
		if !found {
			apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("no armed webhook for this token"))
			return
		}
		if !allowed {
			apiutil.WriteErrorResponse(w, &apperrors.AppError{
				Code:       "METHOD_NOT_ALLOWED",
				Message:    "webhook does not accept this method",
				StatusCode: nethttp.StatusMethodNotAllowed,
			})
			return
		}

		// Address allowlist, before anything is read. A caller that is not on
		// the list gets the same 404 an unknown token does: confirming that a
		// token exists is the one thing a rejection should not do.
		if !registration.AllowsIP(callerIP(r)) {
			apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("no armed webhook for this token"))
			return
		}

		// Read the body as the trigger declared it. A mismatch is the caller's
		// problem, not the flow's: no run starts, so nothing downstream ever
		// sees a payload that is not what the port promised.
		body := readWebhookBody(r)

		// Signature next, over the raw bytes: the HMAC the sender computed is
		// over what it sent, so it has to be checked before any decoding turns
		// those bytes into something else.
		if registration.VerifiesSignatures() {
			supplied := r.Header.Get(registration.SignatureHeader)
			if !registration.VerifySignature(supplied, body) {
				apiutil.WriteErrorResponse(w, &apperrors.AppError{
					Code:       "UNAUTHORIZED",
					Message:    "signature does not match",
					StatusCode: nethttp.StatusUnauthorized,
				})
				return
			}
		}

		payload, err := webhookPayload(r, body, registration.BodyFormat)
		if err != nil {
			apiutil.WriteErrorResponse(w, &apperrors.AppError{
				Code:       "BAD_REQUEST",
				Message:    err.Error(),
				StatusCode: nethttp.StatusBadRequest,
			})
			return
		}
		requestID := uuid.New().String()
		msg := &engine.Msg{
			ID:      requestID,
			Payload: payload,
			Topic:   token,
			Meta:    webhookMeta(r, body, requestID),
		}

		if !registration.Synchronous {
			registration.Emit(msg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(nethttp.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "accepted"}})
			return
		}

		// The trigger's response timeout owns this request. A model-backed flow
		// routinely outlives the server-wide write deadline, which otherwise
		// closes a healthy Assist or webhook request at 30 seconds.
		_ = nethttp.NewResponseController(w).SetWriteDeadline(time.Time{})

		// Synchronous mode: open a response slot before starting the run so a
		// fast flow cannot answer before the handler is listening.
		responses, release := engine.Responses().Register(requestID)
		defer release()
		registration.Emit(msg)

		select {
		case resp := <-responses:
			writeFlowResponse(w, resp)
		case <-r.Context().Done():
			// The client hung up; the run continues on its own.
			return
		case <-time.After(registration.ResponseTimeout):
			apiutil.WriteErrorResponse(w, &apperrors.AppError{
				Code:       "GATEWAY_TIMEOUT",
				Message:    "flow did not produce a response in time",
				StatusCode: nethttp.StatusGatewayTimeout,
			})
		}
	}
}

// writeFlowResponse sends the response a flow produced.
func writeFlowResponse(w nethttp.ResponseWriter, resp *engine.HTTPResponse) {
	if resp == nil {
		apiutil.WriteErrorResponse(w, &apperrors.AppError{
			Code:       "BAD_GATEWAY",
			Message:    "flow produced an empty response",
			StatusCode: nethttp.StatusBadGateway,
		})
		return
	}
	for name, value := range resp.Headers {
		w.Header().Set(name, value)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	status := resp.Status
	if status < 100 || status > 599 {
		status = nethttp.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}

// callerIP is the address an allowlist is checked against: the peer karakuri is
// actually talking to, never a header.
//
// X-Forwarded-For is written by whoever is calling, so trusting it would let
// anyone spell their way onto the list. Behind a reverse proxy that means the
// allowlist sees the proxy, and the right place to filter is the proxy, which
// is the only party that knows the real client.
func callerIP(r *nethttp.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// readWebhookBody reads the request body up to the size cap.
func readWebhookBody(r *nethttp.Request) []byte {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		return nil
	}
	return body
}

// webhookPayload builds the trigger payload from a request, the way the
// trigger's body format asks for.
//
// The format is taken literally, and a body that does not match it is an error
// rather than something else handed downstream. The trigger's output port tells
// the rest of the graph what a run carries; reading a JSON webhook's body as
// text because it failed to parse would make that a guess. An unset format
// reads as JSON, which is what the old automatic mode did for the case it was
// almost always used for.
func webhookPayload(r *nethttp.Request, body []byte, format string) (any, error) {
	switch format {
	case "text":
		return string(body), nil

	case "binary":
		return body, nil

	case "query":
		params := map[string]any{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				params[key] = values[0]
			}
		}
		return params, nil

	default:
		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("body is not valid JSON: %w", err)
		}
		return parsed, nil
	}
}

// webhookMeta describes the request for scripts and expressions. The full
// request detail lives here rather than in the payload so the payload keeps
// its established shape.
func webhookMeta(r *nethttp.Request, body []byte, requestID string) map[string]any {
	query := map[string]any{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}

	// Headers are lowercased for stable lookup. Authorization and cookies are
	// withheld: run events are persisted and shown in the editor, and a
	// credential that lands there outlives the request that carried it.
	headers := map[string]any{}
	for name := range r.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" {
			continue
		}
		headers[lower] = r.Header.Get(name)
	}

	return map[string]any{
		"source":       "webhook",
		"method":       r.Method,
		"remote_addr":  r.RemoteAddr,
		"content_type": r.Header.Get("Content-Type"),
		"user_agent":   r.Header.Get("User-Agent"),
		"path":         r.URL.Path,
		"query":        query,
		"headers":      headers,
		"body":         string(body),

		engine.RequestIDMeta: requestID,
	}
}
