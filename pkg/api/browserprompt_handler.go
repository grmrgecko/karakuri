package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/browserprompt"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// RegisterBrowserPromptRoutes exposes only request-scoped capability operations.
func RegisterBrowserPromptRoutes(r chi.Router, manager *browserprompt.Manager) {
	r.Route("/api/browser_prompts", func(rr chi.Router) {
		rr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Referrer-Policy", "no-referrer")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				next.ServeHTTP(w, r)
			})
		})
		token := func(r *http.Request) string {
			value := r.Header.Get("Authorization")
			if !strings.HasPrefix(value, "Bearer ") {
				return ""
			}
			return strings.TrimPrefix(value, "Bearer ")
		}
		fail := func(w http.ResponseWriter, err error) {
			status := http.StatusBadRequest
			if errors.Is(err, browserprompt.ErrGone) {
				status = http.StatusGone
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": err.Error()})
		}
		rr.Get("/current", func(w http.ResponseWriter, r *http.Request) {
			view, err := manager.Read(token(r))
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(view)
		})
		rr.Post("/response", func(w http.ResponseWriter, r *http.Request) {
			var reply struct {
				Button string `json:"button"`
				Text   string `json:"text"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&reply); err != nil {
				fail(w, errors.New("invalid browser prompt response"))
				return
			}
			if decoder.Decode(&struct{}{}) != io.EOF {
				fail(w, errors.New("browser prompt response must contain one JSON object"))
				return
			}
			if err := manager.Answer(token(r), reply.Button, reply.Text); err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		rr.Get("/assets/{name}", func(w http.ResponseWriter, r *http.Request) {
			a, b, err := manager.Attachment(token(r), chi.URLParam(r, "name"))
			if err != nil {
				fail(w, err)
				return
			}
			disposition := "attachment"
			kind := "application/octet-stream"
			if flow.IsPromptImage(a.Type) {
				disposition = "inline"
				kind = a.Type
			}
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
			_, _ = w.Write(b)
		})
	})
}
