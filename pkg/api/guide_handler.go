package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/docs"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// Guides are the reference pages compiled into the binary and rendered for the
// help pages and the editor panels that link to them. They need only a signed-in user, not a permission of their own — the pages
// describe how the product works and carry nothing about this installation, and
// gating them on any one subsystem would hide the MQTT page from a dashboard
// author who cannot open a flow. The same rule covers the markdown endpoint
// below, which renders text an author wrote rather than a page compiled in.

// GuideListHandler returns the interactor that lists the guides shipped in
// the binary, for the help index.
func GuideListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]docs.Guide]) error {
		out.Data = docs.Guides()
		return nil
	})
	u.SetTitle("List Guides")
	u.SetDescription("Lists the guides built into the server with their titles, groups, and summaries.")
	u.SetTags("Guides")
	return u
}

// GuideGetInput identifies the guide to return.
type GuideGetInput struct {
	ID string `path:"id" description:"Guide identifier, such as \"scripts\"."`
}

// GuideGetHandler returns the interactor that serves one rendered guide.
func GuideGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GuideGetInput, out *apiutil.Success[docs.Document]) error {
		doc, ok := docs.Get(in.ID)
		if !ok {
			return apperrors.ErrNotFound
		}
		out.Data = doc
		return nil
	})
	u.SetTitle("Get Guide")
	u.SetDescription("Returns a reference page rendered from the documentation built into the server.")
	u.SetTags("Guides")
	return u
}

// maxMarkdownBytes bounds a render request. Notes are annotations, not
// documents, and a cap keeps a runaway paste from turning the editor's live
// preview into a parser workload.
const maxMarkdownBytes = 64 << 10

// MarkdownRenderInput carries the markdown to render.
type MarkdownRenderInput struct {
	Markdown string `json:"markdown" description:"Markdown source to render."`
}

// MarkdownRender is the rendered result.
type MarkdownRender struct {
	// HTML is safe to insert directly: raw markup in the source is escaped
	// rather than passed through, which is what lets the editor render a note
	// an author typed.
	HTML string `json:"html" description:"Rendered HTML."`
}

// MarkdownRenderHandler returns the interactor that renders markdown the server
// did not compile in — note nodes on a canvas. The frontend carries no markdown
// parser, and keeping the rendering here means a note reads exactly the way the
// reference pages do, escaping included.
func MarkdownRenderHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MarkdownRenderInput, out *apiutil.Success[MarkdownRender]) error {
		if len(in.Markdown) > maxMarkdownBytes {
			return apperrors.ErrValidation.WithMessage("markdown source is too large to render")
		}
		out.Data = MarkdownRender{HTML: docs.Render(in.Markdown)}
		return nil
	})
	u.SetTitle("Render Markdown")
	u.SetDescription("Renders markdown to HTML with the same renderer the built-in documentation uses.")
	u.SetTags("Guides")
	return u
}

// RegisterGuideRoutes registers the reference documentation endpoints.
func RegisterGuideRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/guides",
			nethttp.NewHandler(GuideListHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/guides/{id}",
			nethttp.NewHandler(GuideGetHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/guides/markdown",
			nethttp.NewHandler(MarkdownRenderHandler()))
}
