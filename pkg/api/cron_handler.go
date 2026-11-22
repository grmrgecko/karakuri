package api

import (
	"context"
	"net/http"
	"time"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// previewOccurrences is how many upcoming run times the editor preview shows.
const previewOccurrences = 5

// CronPreviewInput is the expression and zone to explain.
type CronPreviewInput struct {
	Expr     string `query:"expr" description:"Cron expression to describe."`
	Location string `query:"location" description:"IANA time zone the schedule is evaluated in."`
}

// CronPreview explains a cron expression for the editor: whether it parses,
// what it means in English, and when it next fires.
type CronPreview struct {
	Valid       bool     `json:"valid" description:"Whether the expression and time zone parse."`
	Error       string   `json:"error,omitempty" description:"Why the expression was rejected."`
	Description string   `json:"description,omitempty" description:"Plain English reading of the schedule."`
	Fields      []string `json:"fields,omitempty" description:"Positional field names for the expression."`
	Next        []string `json:"next,omitempty" description:"Upcoming run times in the configured zone, RFC3339."`
}

// CronPreviewHandler returns the interactor that explains a cron expression.
// It uses the same parser the schedule node runs on, so the preview cannot
// disagree with what the engine will do.
func CronPreviewHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in CronPreviewInput, out *apiutil.Success[*CronPreview]) error {
		preview := &CronPreview{Fields: cronspec.FieldLabels(in.Expr)}

		if err := cronspec.Validate(in.Expr, in.Location); err != nil {
			preview.Error = err.Error()
			out.Data = preview
			return nil
		}

		preview.Valid = true
		preview.Description = cronspec.Describe(in.Expr)
		next, err := cronspec.Next(in.Expr, in.Location, time.Now(), previewOccurrences)
		if err != nil {
			preview.Error = err.Error()
			out.Data = preview
			return nil
		}
		for _, at := range next {
			preview.Next = append(preview.Next, at.Format(time.RFC3339))
		}
		out.Data = preview
		return nil
	})
	u.SetTitle("Preview Cron Schedule")
	u.SetDescription("Validates a cron expression and returns its English description and next run times.")
	u.SetTags("NodeTypes")
	return u
}

// RegisterCronRoutes registers the cron preview endpoint.
func RegisterCronRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/nodetypes/cron/preview",
			nethttp.NewHandler(CronPreviewHandler()))
}
