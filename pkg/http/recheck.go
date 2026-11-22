package http

import (
	nethttp "net/http"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/mq"
)

// statusRecorder remembers the status a handler wrote.
type statusRecorder struct {
	nethttp.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush passes through for handlers that stream, such as MCP responses.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(nethttp.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to http.ResponseController, which the
// streaming handlers use to flush and clear deadlines.
func (s *statusRecorder) Unwrap() nethttp.ResponseWriter { return s.ResponseWriter }

// recheckSockets asks the WebSocket hub to revalidate open sockets after every
// successful write through the API or MCP. Sessions, roles, groups, shares,
// and dashboard policy all change through those writes, and an open socket
// must lose what the change revokes instead of keeping what it subscribed to.
// The hub batches the requests, so a burst of writes costs one pass.
func recheckSockets(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.Method {
		case nethttp.MethodGet, nethttp.MethodHead, nethttp.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: nethttp.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status < 400 {
			mq.WS.Recheck()
		}
	})
}
