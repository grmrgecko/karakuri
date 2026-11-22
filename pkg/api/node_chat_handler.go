package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/web"
)

// NodeChatHandler streams a configured draft node's replies without running its output branches.
func NodeChatHandler(w http.ResponseWriter, r *http.Request) {
	f, err := loadFlow(r.Context(), chi.URLParam(r, "id"), models.FlowAccessEdit)
	if err != nil {
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			apiutil.WriteErrorResponse(w, appErr)
		} else {
			apiutil.WriteErrorResponse(w, apperrors.ErrInternal)
		}
		return
	}
	var input flowops.NodeChatOptions
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("invalid chat request: "+err.Error()))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("chat request must contain one JSON object"))
		return
	}
	input.RunAs = callerID(r.Context())
	opts, nt, err := flowops.PrepareNodeChat(f, chi.URLParam(r, "node_id"), input)
	if err != nil {
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			apiutil.WriteErrorResponse(w, appErr)
		} else {
			apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage(err.Error()))
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	var mu sync.Mutex
	encoder := json.NewEncoder(w)
	emit := func(event any) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err := encoder.Encode(event); err != nil {
			cancel()
			return
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			cancel()
		}
	}
	emit(map[string]any{"type": "start"})
	err = flowops.RunNodeChat(ctx, *opts, nt, func(port string, msg *engine.Msg) {
		if msg == nil {
			return
		}
		switch port {
		case "transcript":
			emit(msg.Payload)
		case "out":
			emit(map[string]any{"type": "result", "output": msg.Payload, "meta": msg.Meta})
		}
	})
	if err != nil {
		// Deadline errors still reach connected clients; disconnected clients
		// cannot consume a final event.
		mu.Lock()
		if r.Context().Err() == nil {
			_ = encoder.Encode(map[string]any{"type": "error", "message": err.Error()})
			_ = http.NewResponseController(w).Flush()
		}
		mu.Unlock()
		return
	}
	emit(map[string]any{"type": "done"})
}

// RegisterNodeChatRoutes requires the same authority as executing an edited flow.
func RegisterNodeChatRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/nodes/{node_id}/chat", http.HandlerFunc(NodeChatHandler))
}
