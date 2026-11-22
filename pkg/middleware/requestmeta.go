package middleware

import (
	"context"
	"net"
	"net/http"
)

const requestMetaKey contextKeyType = "requestMeta"

// RequestMeta carries the client's IP address and User-Agent through the request context.
type RequestMeta struct {
	IPAddress string
	UserAgent string
}

// InjectRequestMeta extracts the client IP (with port stripped) and User-Agent
// from the request and stores them in context for downstream handlers.
func InjectRequestMeta() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if host, _, err := net.SplitHostPort(ip); err == nil {
				ip = host
			}

			meta := &RequestMeta{
				IPAddress: ip,
				UserAgent: r.UserAgent(),
			}

			ctx := context.WithValue(r.Context(), requestMetaKey, meta)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetRequestMeta retrieves the request metadata from the context, or nil.
func GetRequestMeta(ctx context.Context) *RequestMeta {
	if meta, ok := ctx.Value(requestMetaKey).(*RequestMeta); ok {
		return meta
	}
	return nil
}
