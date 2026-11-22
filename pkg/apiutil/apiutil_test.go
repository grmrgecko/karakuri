package apiutil

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// TestWriteJSONEnvelopes locks the response body every handler produces. The
// frontend's client reads `data`, `meta`, and `message` off every successful
// response, so their presence and spelling are the API contract; a handler
// writing a bare value must stay bare, with no status or envelope injected
// around it.
func TestWriteJSONEnvelopes(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}

	cases := []struct {
		name  string
		value any
		want  string
		why   string
	}{
		{
			name:  "a bare value is written as it is",
			value: map[string]any{"message": "hello", "count": 42},
			want:  `{"count":42,"message":"hello"}`,
			why:   "endpoints outside the envelope must not gain a status field",
		},
		{
			name:  "Success carries the payload under data",
			value: Success[item]{Data: item{Name: "test"}},
			want:  `{"data":{"name":"test"}}`,
			why:   "the client reads every resource off data",
		},
		{
			name:  "a listing carries its page under meta",
			value: Success[[]string]{Data: []string{"a", "b"}, Meta: MakePageMeta(20, 100, 2)},
			want:  `{"data":["a","b"],"meta":{"pagination":{"total":100,"limit":20,"next":3,"previous":1}}}`,
			why:   "pagination controls are drawn from meta alone",
		},
		{
			name:  "a mutation may answer with a message and no data",
			value: Success[any]{Message: "Deleted"},
			want:  `{"data":null,"message":"Deleted"}`,
			why:   "data has no omitempty, so it stays present and null rather than vanishing from the schema",
		},
	}

	for _, c := range cases {
		w := httptest.NewRecorder()
		WriteJSON(w, http.StatusOK, c.value)

		require.Equalf(t, http.StatusOK, w.Code, "%s: the requested status must be written", c.name)
		assert.Equalf(t, "application/json", w.Header().Get("Content-Type"),
			"%s: every response is JSON", c.name)
		assert.JSONEqf(t, c.want, w.Body.String(), "%s: %s", c.name, c.why)
	}

	// A value the encoder cannot represent has to become a server error rather
	// than a 200 with a truncated body: encoding starts after the status is
	// written, so this is the one path where the two can disagree.
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusOK, math.Inf(1))
	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a value that cannot be encoded must answer 500, not a truncated 200")
}

// TestWriteErrorResponse locks the failure body and its status. The status
// comes from the error rather than the call site, and a bare sentinel still
// has to explain itself, since a blank message reaches the user as a blank
// toast.
func TestWriteErrorResponse(t *testing.T) {
	cases := []struct {
		name       string
		err        *apperrors.AppError
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "a derived error keeps its own message",
			err:        apperrors.ErrNotFound.WithMessage("flow not found"),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantMsg:    "flow not found",
		},
		{
			name:       "a bare sentinel falls back to its default message",
			err:        apperrors.ErrInternal,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error",
		},
	}

	for _, c := range cases {
		w := httptest.NewRecorder()
		WriteErrorResponse(w, c.err)

		require.Equalf(t, c.wantStatus, w.Code, "%s: the status must come from the error", c.name)
		assert.Equalf(t, "application/json", w.Header().Get("Content-Type"),
			"%s: an error body is JSON like any other", c.name)

		var body map[string]any
		require.NoErrorf(t, json.Unmarshal(w.Body.Bytes(), &body), "%s: the error body must decode", c.name)
		assert.Equalf(t, c.wantCode, body["error"], "%s: clients switch on the code", c.name)
		assert.Equalf(t, c.wantMsg, body["message"], "%s: a blank message reaches the user as a blank toast", c.name)
	}
}

// TestMakePageMeta covers the page arithmetic behind the previous and next
// controls. Zero means "no such page", which is what the frontend hides the
// control on, so the boundaries are the whole point: 100 items at 20 a page is
// 5 pages, and page 5 has nothing after it.
func TestMakePageMeta(t *testing.T) {
	cases := []struct {
		name               string
		limit, total, page int64
		previous, next     int64
		why                string
	}{
		{
			name: "the first page has nothing before it", limit: 20, total: 100, page: 1,
			previous: 0, next: 2,
			why: "zero hides the previous control rather than linking to page 0",
		},
		{
			name: "a middle page links both ways", limit: 20, total: 100, page: 3,
			previous: 2, next: 4,
		},
		{
			name: "the last page has nothing after it", limit: 20, total: 100, page: 5,
			previous: 4, next: 0,
			why: "100 items at 20 a page is exactly 5 pages",
		},
		{
			name: "a partial last page still ends the run", limit: 20, total: 81, page: 5,
			previous: 4, next: 0,
			why: "81 items fill four pages and leave one item on the fifth",
		},
	}

	for _, c := range cases {
		p := MakePageMeta(c.limit, c.total, c.page).Pagination
		assert.Equalf(t, c.previous, p.Previous, "previous page for %q: %s", c.name, c.why)
		assert.Equalf(t, c.next, p.Next, "next page for %q: %s", c.name, c.why)
		assert.Equalf(t, c.total, p.Total, "the total must be reported unchanged for %q", c.name)
		assert.Equalf(t, c.limit, p.Limit, "the page size must be reported unchanged for %q", c.name)
	}
}
