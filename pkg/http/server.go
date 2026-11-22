package http

import (
	"context"
	"fmt"
	"net"
	nethttp "net/http"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"go.uber.org/zap"
)

// HTTPServer manages the application's HTTP listener and routes.
type HTTPServer struct {
	server *nethttp.Server
	router nethttp.Handler
	log    *zap.Logger
	done   chan struct{}
}

// NewHTTPServer creates a configured HTTP server with all routes registered.
func NewHTTPServer(log *zap.Logger) *HTTPServer {
	s := &HTTPServer{log: log}
	s.router = newRouter(log)

	addr := fmt.Sprintf("%s:%d", config.C.Server.Host, config.C.Server.Port)
	s.server = &nethttp.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  time.Duration(config.C.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(config.C.Server.WriteTimeout) * time.Second,
	}

	return s
}

// Start launches the HTTP server in a background goroutine and blocks until it
// is accepting connections. The provided context controls graceful shutdown.
func (s *HTTPServer) Start(ctx context.Context) {
	isListening := make(chan bool)
	go s.StartWithIsListening(ctx, isListening)
	<-isListening
}

// StartWithIsListening starts the HTTP server and signals on the provided channel
// once it is accepting connections. Shutdown is triggered when the context is canceled.
func (s *HTTPServer) StartWithIsListening(ctx context.Context, isListening chan bool) {
	s.done = make(chan struct{})

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.C.Server.ShutdownTimeout)*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.log.Error("server shutdown error", zap.Error(err))
		}
	}()

	s.log.Info("starting server", zap.String("addr", s.server.Addr))
	l, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		s.log.Fatal("listen failed", zap.Error(err))
	}
	isListening <- true

	if err := s.server.Serve(l); err != nil && err != nethttp.ErrServerClosed {
		s.log.Error("server error", zap.Error(err))
	}
	s.log.Info("server stopped")
	close(s.done)
}

// Wait blocks until the server has fully stopped.
func (s *HTTPServer) Wait() {
	if s.done != nil {
		<-s.done
	}
}
