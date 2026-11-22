package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/render"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// renderDimension bounds a render request. The floor keeps a dashboard
// legible; the ceiling is 8K, past which a page costs more memory than any
// display can use.
const (
	minRenderDimension = 100
	maxRenderDimension = 7680
)

// renderAvailable reports whether the render engine is running. The public
// render URLs are registered whatever the server is running, so a build with
// the subsystem off answers plainly instead of failing at the first request.
func renderAvailable() bool {
	return render.R != nil
}

// requireDashboardAccess resolves a dashboard and checks the caller may act on
// it at the given level. Render permissions say what a user may do with
// renders at all; this says which dashboards they may do it to.
func requireDashboardAccess(ctx context.Context, ref, level string) (*models.Dashboard, error) {
	dashboard, err := resolveDashboard(ctx, ref)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}

	// The manage permission counts here as it does on the dashboard's own
	// routes, so whoever may open or edit a dashboard may render it.
	if !dashops.CanAccess(ctx, callerID(ctx), dashboard, level) {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	return dashboard, nil
}

// RenderCreateJobInput asks for one render of a dashboard.
type RenderCreateJobInput struct {
	ID         string            `path:"id" description:"Dashboard UUID or slug."`
	Width      int               `json:"width" default:"1920" minimum:"100" maximum:"7680" description:"Render width in pixels."`
	Height     int               `json:"height" default:"1080" minimum:"100" maximum:"7680" description:"Render height in pixels."`
	Format     string            `json:"format" default:"png" enum:"png,pdf,jpg,gif,bmp,tiff" description:"Output format."`
	WebhookURL string            `json:"webhook_url" description:"URL notified when the render settles."`
	Variables  map[string]string `json:"variables,omitempty" description:"Dashboard variable values to render with, by variable name; each an option value or label."`
}

// RenderJobData is a queued job plus the token that collects its result.
type RenderJobData struct {
	ID          uuid.UUID `json:"id" description:"Render job UUID."`
	DashboardID uuid.UUID `json:"dashboard_id" description:"Dashboard UUID."`
	Status      string    `json:"status" description:"Job status."`
	Width       int       `json:"width" description:"Render width in pixels."`
	Height      int       `json:"height" description:"Render height in pixels."`
	Format      string    `json:"format" description:"Output format."`
	Token       string    `json:"token" description:"Token that fetches the result; shown once."`
	ResultURL   string    `json:"result_url" description:"Where the finished render is collected."`
}

// RenderCreateJobHandler returns the interactor that queues a render.
func RenderCreateJobHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderCreateJobInput, out *apiutil.Success[RenderJobData]) error {
		// A render shows what a viewer sees, so viewing is the access it takes.
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessRead)
		if err != nil {
			return err
		}

		if in.Format != "" && !render.ValidFormat(in.Format) {
			return apperrors.ErrValidation.WithMessage("unsupported render format")
		}
		if !renderAvailable() {
			return apperrors.ErrServiceUnavailable.WithMessage("rendering is not available on this server")
		}

		// Queue it. A full queue is reported as such rather than swallowed:
		// the caller can retry, and a silent backlog only delays the failure.
		variables, err := dashops.ResolveVariableValues(dashboard, false, in.Variables)
		if err != nil {
			return err
		}
		job, token, err := render.R.CreateJob(dashboard.ID, in.Width, in.Height, in.Format, in.WebhookURL, nil, variables)
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}

		// Build the response. The token is returned once and stored hashed.
		out.Data = RenderJobData{
			ID:          job.ID,
			DashboardID: job.DashboardID,
			Status:      job.Status,
			Width:       job.Width,
			Height:      job.Height,
			Format:      job.Format,
			Token:       token,
			ResultURL:   fmt.Sprintf("/dashboard/%s/render/job/%s.%s", job.DashboardID, job.ID, job.Format),
		}
		return nil
	})
	u.SetTitle("Create Render Job")
	u.SetDescription("Queues a dashboard render and returns the job with a one-time token for its result.")
	u.SetTags("Renders")
	return u
}

// RenderListJobsInput names the dashboard whose renders to list.
type RenderListJobsInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// RenderListJobsHandler returns the interactor that lists a dashboard's renders.
func RenderListJobsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderListJobsInput, out *apiutil.Success[[]*models.RenderJob]) error {
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessRead)
		if err != nil {
			return err
		}

		jobs, err := models.ListRenderJobsByDashboardID(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = jobs
		return nil
	})
	u.SetTitle("List Render Jobs")
	u.SetDescription("Returns the render jobs of a dashboard, newest first.")
	u.SetTags("Renders")
	return u
}

// RenderCancelJobInput names the render job to stop.
type RenderCancelJobInput struct {
	ID    string `path:"id" description:"Dashboard UUID or slug."`
	JobID string `path:"jobId" format:"uuid" description:"Render job UUID."`
}

// RenderCancelJobHandler returns the interactor that cancels a queued render.
func RenderCancelJobHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderCancelJobInput, out *apiutil.Success[any]) error {
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessRead)
		if err != nil {
			return err
		}

		jobID, perr := uuid.Parse(in.JobID)
		if perr != nil {
			return apperrors.ErrValidation.WithMessage("invalid job ID")
		}
		job, jerr := models.FindRenderJob(jobID)
		if jerr != nil || job.DashboardID != dashboard.ID {
			return apperrors.ErrNotFound.WithMessage("render job not found")
		}

		// Only work that has not settled can be called off. A worker holding
		// the page checks the row before it writes, so cancelling one in
		// flight drops its result.
		if job.Status != models.RenderStatusPending && job.Status != models.RenderStatusProcessing {
			return apperrors.ErrValidation.WithMessage("render job has already finished")
		}
		job.Status = models.RenderStatusFailed
		job.ErrorMsg = "cancelled"
		if err := models.SaveRenderJob(job); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Render job cancelled"
		return nil
	})
	u.SetTitle("Cancel Render Job")
	u.SetDescription("Cancels a render job that has not finished.")
	u.SetTags("Renders")
	return u
}

// RenderListSchedulesInput names the dashboard whose schedules to list.
type RenderListSchedulesInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// RenderListSchedulesHandler returns the interactor that lists render schedules.
func RenderListSchedulesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderListSchedulesInput, out *apiutil.Success[[]*models.RenderSchedule]) error {
		// A schedule is configuration of the dashboard, not a view of it.
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessEdit)
		if err != nil {
			return err
		}

		schedules, err := models.ListRenderSchedulesByDashboardID(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = schedules
		return nil
	})
	u.SetTitle("List Render Schedules")
	u.SetDescription("Returns the render schedules of a dashboard.")
	u.SetTags("Renders")
	return u
}

// RenderCreateScheduleInput describes a recurring render.
type RenderCreateScheduleInput struct {
	ID         string `path:"id" description:"Dashboard UUID or slug."`
	Name       string `json:"name" description:"Display name for the schedule."`
	CronExpr   string `json:"cron_expr" required:"true" minLength:"1" description:"Cron expression the render fires on."`
	Location   string `json:"location" description:"IANA time zone; empty means server local time."`
	Format     string `json:"format" default:"png" enum:"png,pdf,jpg,gif,bmp,tiff" description:"Output format."`
	Width      int    `json:"width" default:"1920" minimum:"100" maximum:"7680" description:"Render width in pixels."`
	Height     int    `json:"height" default:"1080" minimum:"100" maximum:"7680" description:"Render height in pixels."`
	Enabled    *bool  `json:"enabled,omitempty" description:"Whether the schedule fires."`
	WebhookURL string `json:"webhook_url" description:"URL notified when a scheduled render settles."`
}

// RenderCreateScheduleHandler returns the interactor that adds a render schedule.
func RenderCreateScheduleHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderCreateScheduleInput, out *apiutil.Success[*models.RenderSchedule]) error {
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessEdit)
		if err != nil {
			return err
		}

		// The expression is checked here rather than at the first fire, on the
		// parser the runner uses, so a schedule that cannot run is refused
		// while its author is still looking at it.
		if verr := cronspec.Validate(in.CronExpr, in.Location); verr != nil {
			return apperrors.ErrValidation.WithMessage(verr.Error())
		}
		if in.Format != "" && !render.ValidFormat(in.Format) {
			return apperrors.ErrValidation.WithMessage("unsupported render format")
		}

		enabled := true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		schedule := &models.RenderSchedule{
			DashboardID: dashboard.ID,
			Name:        in.Name,
			CronExpr:    in.CronExpr,
			Location:    in.Location,
			Format:      in.Format,
			Width:       in.Width,
			Height:      in.Height,
			Enabled:     enabled,
			WebhookURL:  in.WebhookURL,
		}
		if err := models.CreateRenderSchedule(schedule); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		// The enabled column defaults to true, and an insert leaves a false
		// to that default, so a schedule created disabled is switched off
		// before the scheduler reads it.
		if !enabled {
			schedule.Enabled = false
			if err := models.SaveRenderSchedule(schedule); err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
		}

		// Arm it now: a schedule that waited for a restart would be a schedule
		// the author could not tell was working.
		render.S.Reload()

		out.Data = schedule
		return nil
	})
	u.SetTitle("Create Render Schedule")
	u.SetDescription("Adds a recurring render of a dashboard.")
	u.SetTags("Renders")
	return u
}

// RenderUpdateScheduleInput patches a render schedule.
type RenderUpdateScheduleInput struct {
	ID         string  `path:"id" description:"Dashboard UUID or slug."`
	SchedID    uint    `path:"schedId" description:"Render schedule ID."`
	Name       *string `json:"name,omitempty" description:"Display name for the schedule."`
	CronExpr   *string `json:"cron_expr,omitempty" description:"Cron expression the render fires on."`
	Location   *string `json:"location,omitempty" description:"IANA time zone."`
	Format     *string `json:"format,omitempty" description:"Output format."`
	Width      *int    `json:"width,omitempty" description:"Render width in pixels."`
	Height     *int    `json:"height,omitempty" description:"Render height in pixels."`
	Enabled    *bool   `json:"enabled,omitempty" description:"Whether the schedule fires."`
	WebhookURL *string `json:"webhook_url,omitempty" description:"URL notified when a scheduled render settles."`
}

// RenderUpdateScheduleHandler returns the interactor that edits a render schedule.
func RenderUpdateScheduleHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderUpdateScheduleInput, out *apiutil.Success[*models.RenderSchedule]) error {
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessEdit)
		if err != nil {
			return err
		}

		schedule, serr := models.FindRenderScheduleByID(in.SchedID)
		if serr != nil || schedule.DashboardID != dashboard.ID {
			return apperrors.ErrNotFound.WithMessage("render schedule not found")
		}

		// Apply the requested changes.
		if in.Name != nil {
			schedule.Name = *in.Name
		}
		if in.CronExpr != nil {
			schedule.CronExpr = *in.CronExpr
		}
		if in.Location != nil {
			schedule.Location = *in.Location
		}
		if verr := cronspec.Validate(schedule.CronExpr, schedule.Location); verr != nil {
			return apperrors.ErrValidation.WithMessage(verr.Error())
		}
		if in.Format != nil {
			if !render.ValidFormat(*in.Format) {
				return apperrors.ErrValidation.WithMessage("unsupported render format")
			}
			schedule.Format = *in.Format
		}
		if in.Width != nil {
			schedule.Width = *in.Width
		}
		if in.Height != nil {
			schedule.Height = *in.Height
		}
		if in.Enabled != nil {
			schedule.Enabled = *in.Enabled
		}
		if in.WebhookURL != nil {
			schedule.WebhookURL = *in.WebhookURL
		}
		if schedule.Width < minRenderDimension || schedule.Width > maxRenderDimension ||
			schedule.Height < minRenderDimension || schedule.Height > maxRenderDimension {
			return apperrors.ErrValidation.WithMessage("render dimensions must be between 100 and 7680 pixels")
		}

		if err := models.SaveRenderSchedule(schedule); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		render.S.Reload()

		out.Data = schedule
		return nil
	})
	u.SetTitle("Update Render Schedule")
	u.SetDescription("Edits a recurring render of a dashboard.")
	u.SetTags("Renders")
	return u
}

// RenderDeleteScheduleInput names the render schedule to remove.
type RenderDeleteScheduleInput struct {
	ID      string `path:"id" description:"Dashboard UUID or slug."`
	SchedID uint   `path:"schedId" description:"Render schedule ID."`
}

// RenderDeleteScheduleHandler returns the interactor that removes a schedule.
func RenderDeleteScheduleHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RenderDeleteScheduleInput, out *apiutil.Success[any]) error {
		dashboard, err := requireDashboardAccess(ctx, in.ID, models.DashboardAccessEdit)
		if err != nil {
			return err
		}

		schedule, serr := models.FindRenderScheduleByID(in.SchedID)
		if serr != nil || schedule.DashboardID != dashboard.ID {
			return apperrors.ErrNotFound.WithMessage("render schedule not found")
		}
		if err := models.DeleteRenderSchedule(schedule.ID); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		render.S.Reload()

		out.Message = "Render schedule deleted"
		return nil
	})
	u.SetTitle("Delete Render Schedule")
	u.SetDescription("Removes a recurring render of a dashboard.")
	u.SetTags("Renders")
	return u
}

// --- Public render URLs ---
//
// These live outside /api because they are addresses rather than API calls:
// what points at them is an <img> tag, a photo frame, or a wiki page, and none
// of those can set a header. Access is the dashboard's own: a public dashboard
// renders for anyone, anything else takes a token in the query string, which is
// what an API key is for.

// RenderDirectHandler renders a dashboard and answers with the file.
// GET /dashboard/{id}/render/{width}x{height}.{format}
func RenderDirectHandler(w http.ResponseWriter, r *http.Request) {
	dashboard, appErr := renderViewable(r, chi.URLParam(r, "id"))
	if appErr != nil {
		apiutil.WriteErrorResponse(w, appErr)
		return
	}

	width, height, format, appErr := parseRenderSpec(chi.URLParam(r, "spec"))
	if appErr != nil {
		apiutil.WriteErrorResponse(w, appErr)
		return
	}
	if !renderAvailable() {
		apiutil.WriteErrorResponse(w, apperrors.ErrServiceUnavailable.
			WithMessage("rendering is not available on this server"))
		return
	}

	// Variables arrive as var-<name> parameters, the same spelling a shared
	// dashboard link uses.
	requested := map[string]string{}
	for key, values := range r.URL.Query() {
		if name, ok := strings.CutPrefix(key, "var-"); ok && len(values) > 0 {
			requested[name] = values[0]
		}
	}
	variables, err := dashops.ResolveVariableValues(dashboard, false, requested)
	if err != nil {
		if !errors.As(err, &appErr) {
			appErr = apperrors.ErrInternal.WithMessage(err.Error())
		}
		apiutil.WriteErrorResponse(w, appErr)
		return
	}

	// Render synchronously: the caller asked for an image, not a receipt.
	job, err := render.R.CreateJobSync(dashboard.ID, width, height, format, variables)
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithMessage(err.Error()))
		return
	}

	serveRender(w, r, job)
}

// RenderJobResultHandler serves a queued render's file to whoever holds its
// token. GET /dashboard/{id}/render/job/{jobId}.{format}?token=
func RenderJobResultHandler(w http.ResponseWriter, r *http.Request) {
	jobID, format, appErr := parseIDSpec(chi.URLParam(r, "jobSpec"), "job")
	if appErr != nil {
		apiutil.WriteErrorResponse(w, appErr)
		return
	}
	id, err := uuid.Parse(jobID)
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("invalid job ID"))
		return
	}

	// The token is the whole credential here: it was minted for this job and
	// returned once, so holding it is what a result URL means.
	token := r.URL.Query().Get("token")
	if token == "" {
		apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("a render token is required"))
		return
	}
	job, err := models.FindRenderJob(id)
	if err != nil || job.Format != format {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("render not found"))
		return
	}
	if !models.CheckRenderToken(job.TokenHash, token) {
		apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("invalid render token"))
		return
	}
	if job.Status != models.RenderStatusCompleted {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.
			WithMessage(fmt.Sprintf("render is %s", job.Status)))
		return
	}

	serveRender(w, r, job)
}

// RenderScheduleResultHandler serves a schedule's latest render. The address
// never changes, which is what a wall display points at.
// GET /dashboard/{id}/render/schedule/{schedId}.{format}
func RenderScheduleResultHandler(w http.ResponseWriter, r *http.Request) {
	dashboard, appErr := renderViewable(r, chi.URLParam(r, "id"))
	if appErr != nil {
		apiutil.WriteErrorResponse(w, appErr)
		return
	}

	idPart, format, appErr := parseIDSpec(chi.URLParam(r, "schedSpec"), "schedule")
	if appErr != nil {
		apiutil.WriteErrorResponse(w, appErr)
		return
	}
	schedID, err := strconv.ParseUint(idPart, 10, 64)
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("invalid schedule ID"))
		return
	}
	schedule, err := models.FindRenderScheduleByID(uint(schedID))
	if err != nil || schedule.DashboardID != dashboard.ID {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("render schedule not found"))
		return
	}

	job, err := models.FindLatestCompletedRenderJobBySchedule(schedule.ID)
	if err != nil || job.Format != format {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.
			WithMessage("this schedule has not produced a render in that format yet"))
		return
	}

	serveRender(w, r, job)
}

// renderViewable resolves the dashboard a public render URL names and checks
// the caller may view it. A private one is reported as missing rather than
// forbidden, so a slug cannot be confirmed by probing. A render key opens
// only the dashboard it names, and only while its owner may view it.
func renderViewable(r *http.Request, ref string) (*models.Dashboard, *apperrors.AppError) {
	dashboard, err := resolveDashboard(r.Context(), ref)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}

	if !middleware.RenderKeyAllows(r.Context(), dashboard.ID) ||
		!dashops.CanView(r.Context(), callerID(r.Context()), dashboard) {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	return dashboard, nil
}

// serveRender writes a finished render's file.
func serveRender(w http.ResponseWriter, r *http.Request, job *models.RenderJob) {
	if job.ResultPath == "" {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("render file not found"))
		return
	}
	if _, err := os.Stat(job.ResultPath); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("render file not found"))
		return
	}
	w.Header().Set("Content-Type", render.ContentTypeForFormat(job.Format))
	// A render is a photograph of one moment; a cached copy of it is a photo
	// of a different one.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, job.ResultPath)
}

// parseRenderSpec reads "{width}x{height}.{format}" out of a URL.
func parseRenderSpec(spec string) (int, int, string, *apperrors.AppError) {
	size, format, ok := splitSpec(spec)
	if !ok {
		return 0, 0, "", apperrors.ErrValidation.
			WithMessage("invalid render spec; expected {width}x{height}.{format}")
	}
	if !render.ValidFormat(format) {
		return 0, 0, "", apperrors.ErrValidation.WithMessage(
			fmt.Sprintf("unsupported format %q; valid formats: %s", format, strings.Join(render.Formats, ", ")))
	}

	parts := strings.SplitN(size, "x", 2)
	if len(parts) != 2 {
		return 0, 0, "", apperrors.ErrValidation.WithMessage("invalid dimensions; expected {width}x{height}")
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil || width < minRenderDimension || width > maxRenderDimension {
		return 0, 0, "", apperrors.ErrValidation.WithMessage("width must be between 100 and 7680")
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil || height < minRenderDimension || height > maxRenderDimension {
		return 0, 0, "", apperrors.ErrValidation.WithMessage("height must be between 100 and 7680")
	}
	return width, height, format, nil
}

// parseIDSpec reads "{id}.{format}" out of a URL. The extension is part of the
// address so a browser, a wiki, and a photo frame all recognise what they are
// being handed.
func parseIDSpec(spec, kind string) (string, string, *apperrors.AppError) {
	id, format, ok := splitSpec(spec)
	if !ok || id == "" {
		return "", "", apperrors.ErrValidation.
			WithMessage(fmt.Sprintf("invalid %s spec; expected {id}.{format}", kind))
	}
	if !render.ValidFormat(format) {
		return "", "", apperrors.ErrValidation.WithMessage("unsupported render format")
	}
	return id, format, nil
}

// splitSpec separates a spec from its extension.
func splitSpec(spec string) (head, format string, ok bool) {
	dot := strings.LastIndex(spec, ".")
	if dot < 0 {
		return "", "", false
	}
	return spec[:dot], spec[dot+1:], true
}

// RegisterRenderRoutes registers render job and schedule management. The public
// render URLs are registered by the HTTP router, outside /api.
func RegisterRenderRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Render jobs.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersRead), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/renders",
			nethttp.NewHandler(RenderListJobsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/renders",
			nethttp.NewHandler(RenderCreateJobHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersDelete), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/renders/{jobId}",
			nethttp.NewHandler(RenderCancelJobHandler()))

	// Render schedules.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersRead), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/render_schedules",
			nethttp.NewHandler(RenderListSchedulesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/render_schedules",
			nethttp.NewHandler(RenderCreateScheduleHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/render_schedules/{schedId}",
			nethttp.NewHandler(RenderUpdateScheduleHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRendersDelete), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/render_schedules/{schedId}",
			nethttp.NewHandler(RenderDeleteScheduleHandler()))
}
