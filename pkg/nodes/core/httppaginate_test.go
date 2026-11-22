package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPaginationAdvancesAParameter drives the node against a real server that
// paginates the way most REST APIs do: a page number in the query and a shorter
// last page. It pins the two things a hand-built loop gets wrong — that the
// merged result is every page's items in order, and that the loop stops on the
// empty page rather than asking forever.
func TestPaginationAdvancesAParameter(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		seen = append(seen, page)
		switch page {
		case "1":
			fmt.Fprint(w, `{"results":[{"id":1},{"id":2}]}`)
		case "2":
			fmt.Fprint(w, `{"results":[{"id":3}]}`)
		default:
			fmt.Fprint(w, `{"results":[]}`)
		}
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`{"url":%q,"pagination":"param","page_param":"page",
		"items_path":"payload.results","max_pages":"10"}`, srv.URL)
	out := runNode(t, HTTPRequestNode{}, cfg, &engine.Msg{})

	require.Len(t, out["out"], 1, "merged pagination emits one message")
	msg := out["out"][0]
	assert.Equal(t, []string{"1", "2", "3"}, seen,
		"the page parameter advances by one and the empty third page is what stops it")
	assert.Equal(t, 3, msg.Meta["pages"], "three requests were made, so three pages are reported")

	// The merged list is written back where it was read from, so a path written
	// against a single page still resolves.
	body, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "the envelope the API sent is kept, not replaced by a bare list")
	results, ok := body["results"].([]any)
	require.True(t, ok, "results is still a list at the same path")
	require.Len(t, results, 3, "both non-empty pages contributed, and the empty one added nothing")
	assert.Equal(t, float64(1), results[0].(map[string]any)["id"], "page order is preserved")
	assert.Equal(t, float64(3), results[2].(map[string]any)["id"], "the last page's item comes last")
}

// TestPaginationFollowsLinks covers the other half of how APIs paginate, and
// the two details that make it work against real ones: a relative link resolves
// against the page that carried it, and a Link header is read per RFC 8288
// rather than by looking for a URL in the string.
func TestPaginationFollowsLinks(t *testing.T) {
	t.Run("a relative link in the body", func(t *testing.T) {
		var passwords []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pass, _ := r.BasicAuth()
			passwords = append(passwords, pass)
			if r.URL.Query().Get("cursor") == "" {
				// Relative, which is what an API that does not know its own
				// public hostname returns.
				fmt.Fprint(w, `{"items":["a"],"next":"?cursor=z"}`)
				return
			}
			fmt.Fprint(w, `{"items":["b"],"next":null}`)
		}))
		defer srv.Close()

		// Credentials in the URL are masked in the page's url metadata, so
		// the link must resolve against the URL actually requested.
		withUser := strings.Replace(srv.URL, "http://", "http://user:secret@", 1)
		cfg := fmt.Sprintf(`{"url":%q,"pagination":"next_url",
			"next_url_path":"payload.next","items_path":"payload.items"}`, withUser)
		out := runNode(t, HTTPRequestNode{}, cfg, &engine.Msg{})

		body := out["out"][0].Payload.(map[string]any)
		assert.Equal(t, []any{"a", "b"}, body["items"],
			"a relative next link resolved against its page, so the second request landed")
		assert.Equal(t, 2, out["out"][0].Meta["pages"], "a null next link ends the loop")
		assert.Equal(t, []string{"secret", "secret"}, passwords,
			"the second page keeps the real password, not the masked one from the url metadata")
	})

	t.Run("a Link header", func(t *testing.T) {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "" {
				// Two entries with the next one second, so the test fails if the
				// parser takes the first URL it finds rather than reading rel.
				// The next entry is relative, which RFC 8288 resolves against
				// the request URL.
				w.Header().Set("Link", fmt.Sprintf(`<%s/?page=1>; rel="prev", </?page=2>; rel="next"`, srv.URL))
				fmt.Fprint(w, `["a"]`)
				return
			}
			fmt.Fprint(w, `["b"]`)
		}))
		defer srv.Close()

		cfg := fmt.Sprintf(`{"url":%q,"pagination":"link_header"}`, srv.URL)
		out := runNode(t, HTTPRequestNode{}, cfg, &engine.Msg{})

		assert.Equal(t, []any{"a", "b"}, out["out"][0].Payload,
			"rel=next was followed, not rel=prev, and a bare array payload merges as the list itself")
		assert.Equal(t, 2, out["out"][0].Meta["pages"], "the second page carries no Link, so the loop ends")
	})
}

// TestLinkHeaderNextReadsEntries locks how a Link header is taken apart. A URL
// may hold commas and a rel may list several relations, so splitting the
// header on commas lost the next link and ended pagination after one page.
func TestLinkHeaderNextReadsEntries(t *testing.T) {
	cases := []struct {
		name   string
		header string
		expect string
	}{
		{name: "a comma inside the URL",
			header: `<https://api.example/items?ids=1,2&page=2>; rel="next"`,
			expect: "https://api.example/items?ids=1,2&page=2"},
		{name: "a comma inside an earlier URL",
			header: `<https://api.example/items?ids=1,2&page=1>; rel="prev", <https://api.example/items?ids=1,2&page=3>; rel="next"`,
			expect: "https://api.example/items?ids=1,2&page=3"},
		{name: "several relations on one link",
			header: `<https://api.example/items?page=2>; rel="next last"`,
			expect: "https://api.example/items?page=2"},
		{name: "a comma inside a quoted parameter",
			header: `<https://api.example/items?page=1>; rel="prev"; title="one, two", <https://api.example/items?page=3>; rel=next`,
			expect: "https://api.example/items?page=3"},
		{name: "no next relation",
			header: `<https://api.example/items?page=1>; rel="prev"`,
			expect: ""},
	}
	for _, c := range cases {
		assert.Equalf(t, c.expect, linkHeaderNext(map[string]any{"Link": c.header}),
			"the next link of a header with %s", c.name)
	}
}

// TestPaginationStopsAtTheCap pins the guard. The loop is driven by what the
// server says, so a server that always claims another page must still end, and
// it must end at exactly the configured number rather than one either side.
func TestPaginationStopsAtTheCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"results":[{"id":1}]}`)
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`{"url":%q,"pagination":"param","page_param":"page",
		"items_path":"payload.results","max_pages":"3"}`, srv.URL)
	out := runNode(t, HTTPRequestNode{}, cfg, &engine.Msg{})

	assert.Equal(t, 3, calls, "an API that never says it is done is stopped by the cap")
	assert.Len(t, out["out"][0].Payload.(map[string]any)["results"], 3, "one item from each page it did fetch")
}

// TestPaginationEmitsAMessagePerPage covers the fan-out form, which exists so a
// downstream branch runs per page. The group frame is what makes that safe: a
// join has to know how many are coming, and the count is only known once the
// loop has finished.
func TestPaginationEmitsAMessagePerPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, `{"results":[{"id":1}]}`)
			return
		}
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`{"url":%q,"pagination":"param","page_param":"page",
		"items_path":"payload.results","page_output":"pages"}`, srv.URL)
	out := runNode(t, HTTPRequestNode{}, cfg, &engine.Msg{})

	require.Len(t, out["out"], 2, "the empty page is still a page, and it is emitted as one")
	for i, msg := range out["out"] {
		require.Len(t, msg.Group, 1, "each page carries this node's own group frame")
		assert.Equal(t, i, msg.Group[0].Index, "pages are numbered in fetch order")
		assert.Equal(t, 2, msg.Group[0].Count, "the count is what a join downstream waits for")
		assert.Equal(t, i+1, msg.Meta["page"], "the metadata numbers pages from one, the way a person would")
	}
}

// TestAPIKeyAuthGoesWhereItIsTold pins the two placements. The query form is
// the one worth checking rather than assuming: the key has to survive the URL
// being rebuilt, which is where a naive implementation loses it.
func TestAPIKeyAuthGoesWhereItIsTold(t *testing.T) {
	var gotHeader, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-API-Key")
		gotQuery = r.URL.Query().Get("api_key")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`{"url":%q,"auth":"header","auth_name":"X-API-Key","token":"s3cr3t"}`, srv.URL)
	_, err := HTTPRequestNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": {}})
	require.NoError(t, err, "a header key is an ordinary request")
	assert.Equal(t, "s3cr3t", gotHeader, "the key lands in the named header")

	cfg = fmt.Sprintf(`{"url":%q,"query":{"existing":"1"},"auth":"query","auth_name":"api_key","token":"s3cr3t"}`, srv.URL)
	_, err = HTTPRequestNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": {}})
	require.NoError(t, err, "a query key is an ordinary request")
	assert.Equal(t, "s3cr3t", gotQuery, "the key survives the query being rebuilt around the configured parameters")
}
