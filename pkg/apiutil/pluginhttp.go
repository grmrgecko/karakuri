package apiutil

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
)

// ctxKey namespaces the request-context values a proxied plugin request
// carries, so a plugin's handlers read the caller's identity the way any
// middleware-authenticated handler would.
type ctxKey int

const (
	// CtxPluginUserID holds the authenticated user's ID, empty if anonymous.
	CtxPluginUserID ctxKey = iota
	// CtxPluginUsername holds the authenticated user's name.
	CtxPluginUsername
	// CtxPluginDashboardGrant holds the ID of the dashboard the host authorized
	// the request against, rather than the caller's own permissions.
	CtxPluginDashboardGrant
	// CtxPluginKeyLimit holds the permission limit of the API key the caller
	// authenticated with, when it is limited.
	CtxPluginKeyLimit
)

// pluginKeyLimit is the limit a proxied request's API key carries.
type pluginKeyLimit struct {
	permissions []string
}

// WithPluginKeyLimit marks a proxied request as made with an API key limited
// to the given permissions.
func WithPluginKeyLimit(r *http.Request, perms []string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), CtxPluginKeyLimit, pluginKeyLimit{permissions: perms}))
}

// PluginKeyLimit returns the permissions a proxied request's API key is
// limited to, and whether it is limited.
func PluginKeyLimit(r *http.Request) ([]string, bool) {
	v, ok := r.Context().Value(CtxPluginKeyLimit).(pluginKeyLimit)
	return v.permissions, ok
}

// PluginHTTPRequest rebuilds a proxied request as a real *http.Request inside
// the plugin process, so a plugin routes with an ordinary chi or swaggest
// service instead of parsing the wire form itself. The host already
// authenticated the caller; identity rides in the context.
func PluginHTTPRequest(method, path, queryString, remoteAddr, userID, username string,
	headers map[string]string, body []byte, dashboardGrant string) (*http.Request, error) {

	u := &url.URL{Path: path, RawQuery: queryString}

	var bodyReader io.Reader = http.NoBody
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	ctx := req.Context()
	if userID != "" {
		ctx = context.WithValue(ctx, CtxPluginUserID, userID)
	}
	if username != "" {
		ctx = context.WithValue(ctx, CtxPluginUsername, username)
	}
	if dashboardGrant != "" {
		ctx = context.WithValue(ctx, CtxPluginDashboardGrant, dashboardGrant)
	}
	return req.WithContext(ctx), nil
}

// PluginResponseWriter buffers a whole response so it can cross the process
// boundary as flat primitives.
type PluginResponseWriter struct {
	statusCode int
	headers    http.Header
	body       bytes.Buffer
}

// NewPluginResponseWriter creates a buffering response writer.
func NewPluginResponseWriter() *PluginResponseWriter {
	return &PluginResponseWriter{
		statusCode: http.StatusOK,
		headers:    make(http.Header),
	}
}

// Header implements http.ResponseWriter.
func (w *PluginResponseWriter) Header() http.Header { return w.headers }

// Write implements http.ResponseWriter.
func (w *PluginResponseWriter) Write(data []byte) (int, error) { return w.body.Write(data) }

// WriteHeader implements http.ResponseWriter.
func (w *PluginResponseWriter) WriteHeader(statusCode int) { w.statusCode = statusCode }

// Result returns the captured response. Multi-valued headers collapse to their
// first value, which is all the wire form carries.
func (w *PluginResponseWriter) Result() (statusCode int, headers map[string]string, body []byte) {
	flat := make(map[string]string, len(w.headers))
	for k := range w.headers {
		flat[k] = w.headers.Get(k)
	}
	return w.statusCode, flat, w.body.Bytes()
}

// PluginUserID returns the authenticated user's ID from a proxied request.
func PluginUserID(r *http.Request) string {
	v, _ := r.Context().Value(CtxPluginUserID).(string)
	return v
}

// PluginUsername returns the authenticated user's name from a proxied request.
func PluginUsername(r *http.Request) string {
	v, _ := r.Context().Value(CtxPluginUsername).(string)
	return v
}

// PluginDashboardGrant returns the dashboard the host authorized this request
// against, empty when it authorized none through one. A route keyed by
// dashboard must compare this with its own path parameter before trusting it.
func PluginDashboardGrant(r *http.Request) string {
	v, _ := r.Context().Value(CtxPluginDashboardGrant).(string)
	return v
}
