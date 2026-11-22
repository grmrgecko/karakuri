package apiutil

import (
	"encoding/json"
	"net/http"
	"strconv"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// Success is the standard wrapper for all successful API responses.
type Success[T any] struct {
	Data    T      `json:"data" description:"Primary payload."`
	Meta    *Meta  `json:"meta,omitempty" description:"Response metadata."`
	Message string `json:"message,omitempty" description:"Human-readable summary."`
}

// Meta contains non-resource metadata.
type Meta struct {
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Pagination follows the standard next/prev/limit/total pattern.
type Pagination struct {
	Next     int64 `json:"next,omitempty" description:"Page number for the next page."`
	Previous int64 `json:"previous,omitempty" description:"Page number for the previous page."`
	Limit    int64 `json:"limit" description:"Items per page."`
	Total    int64 `json:"total" description:"Total number of items."`
}

// MakePageMeta builds a Meta with calculated Next/Previous page numbers.
func MakePageMeta(limit, total, page int64) *Meta {
	p := &Pagination{Limit: limit, Total: total}
	if page > 1 {
		p.Previous = page - 1
	}
	if page*limit < total {
		p.Next = page + 1
	}
	return &Meta{Pagination: p}
}

// WriteJSON writes a JSON response with the given status code. The data value
// is encoded as-is with no merging or flattening.
func WriteJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	buf, err := json.Marshal(data)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(apperrors.ErrInternal)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(buf)
	_, _ = w.Write([]byte("\n"))
}

// WriteErrorResponse writes a JSON error response derived from an AppError.
func WriteErrorResponse(w http.ResponseWriter, err *apperrors.AppError) {
	WriteJSON(w, err.StatusCode, err)
}

// DecodeJSON reads the request body into the given value.
func DecodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// GetPagination extracts page and limit query parameters with defaults.
func GetPagination(r *http.Request) (int64, int64) {
	page, _ := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit < 1 || limit > 100 {
		limit = 20
	}

	return page, limit
}
