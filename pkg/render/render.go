// Package render photographs dashboards with a headless browser. A render is
// a viewer like any other: the browser opens the dashboard's own view route,
// the deployed layout decides what it may read, and what comes back is a file
// on disk. That is what makes a dashboard embeddable in a wiki, mailable, and
// displayable on a photo frame or an e-ink panel that speaks nothing but HTTP.
//
// The work is queued rather than done inline because it outlives the request
// that asked for it: a schedule fires with nobody watching, a webhook is
// delivered afterwards, and the file is collected later by URL.
package render

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"go.uber.org/zap"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// R is the package-level render engine handlers and the scheduler use.
var R *Render

// readyAttr is the flag the dashboard view sets once its widgets are mounted.
// Waiting for it is what stops a screenshot catching the loading state; a page
// that never sets it still renders, one stability wait later.
const readyAttr = "[data-dashboard-ready='true']"

// readyWait bounds how long a render waits for that flag before falling back
// to stability alone.
const readyWait = 10 * time.Second

// sweepInterval is how often finished renders are swept off disk.
const sweepInterval = 15 * time.Minute

// Render owns the headless browser and the worker pool that drives it.
type Render struct {
	log     *zap.Logger
	workers int
	jobs    chan uuid.UUID
	stop    chan struct{}
	wg      sync.WaitGroup

	// waiters unblocks a synchronous render when its job settles.
	waiters sync.Map // uuid.UUID → chan struct{}
	// tokens holds each queued job's raw render token until the worker puts it
	// in the URL. Only the hash is stored, so this is the one copy.
	tokens sync.Map // uuid.UUID → string

	// The browser is launched on demand and shared by every worker: a server
	// that never renders never starts one, and a browser that died between
	// jobs is replaced rather than failing every render after it.
	browserMu sync.Mutex
	launcher  *launcher.Launcher
	browser   *rod.Browser
}

// Init builds the render engine from configuration and sets R.
func Init(log *zap.Logger) {
	workers := config.C.Render.Workers
	if workers < 1 {
		workers = 1
	}
	queue := config.C.Render.MaxQueueSize
	if queue <= 0 {
		queue = workers * 4
	}
	R = &Render{
		log:     log,
		workers: workers,
		jobs:    make(chan uuid.UUID, queue),
		stop:    make(chan struct{}),
	}
}

// Start prepares the output directory and starts the workers and the sweeper.
// It does not launch a browser: nothing is spent until something is rendered,
// and an install with no browser installed only finds out when it asks for a
// render rather than at every boot.
func (r *Render) Start() error {
	if err := os.MkdirAll(config.C.Render.StoragePath, 0o755); err != nil {
		return fmt.Errorf("create render storage directory: %w", err)
	}

	for i := 0; i < r.workers; i++ {
		r.wg.Add(1)
		go r.worker(i)
	}

	r.wg.Add(1)
	go r.sweeper()

	// Jobs queued before a restart are still wanted; the tokens they held were
	// never stored, so each gets a fresh one.
	r.requeuePending()

	r.log.Info("render service started", zap.Int("workers", r.workers))
	return nil
}

// Stop drains the workers and closes the browser if one was ever launched.
func (r *Render) Stop() {
	close(r.stop)
	r.wg.Wait()
	r.closeBrowser()
	r.log.Info("render service stopped")
}

// Submit queues a job for a worker. A full queue is refused rather than
// buffered without bound: renders cost a browser page each, and an unbounded
// backlog only delays the failure.
func (r *Render) Submit(jobID uuid.UUID) error {
	select {
	case r.jobs <- jobID:
		return nil
	default:
		return fmt.Errorf("render queue is full")
	}
}

// CreateJob records a render request and queues it. The returned token is the
// caller's receipt: it is what fetches the result and is never stored in the
// clear.
// Variables are resolved option values; the caller checks them against the
// dashboard first.
func (r *Render) CreateJob(dashboardID uuid.UUID, width, height int, format, webhookURL string, scheduleID *uint, variables map[string]string) (*models.RenderJob, string, error) {
	return r.createJob(dashboardID, width, height, format, webhookURL, scheduleID, false, false, variables)
}

func (r *Render) createJob(dashboardID uuid.UUID, width, height int, format, webhookURL string, scheduleID *uint, draft, inspect bool, variables map[string]string) (*models.RenderJob, string, error) {
	// Mint both credentials. The result token goes to the caller; the render
	// token goes to the headless browser and grants a view of this dashboard
	// alone, for as long as the job is being worked on.
	rawToken, tokenHash, err := generateToken()
	if err != nil {
		return nil, "", err
	}
	rawRenderToken, renderTokenHash, err := generateToken()
	if err != nil {
		return nil, "", err
	}

	// Fill in the defaults a caller may have left out.
	if width <= 0 {
		width = 1920
	}
	if height <= 0 {
		height = 1080
	}
	if format == "" {
		format = "png"
	}

	// Persist the job. A scheduled render is kept until the next one of that
	// schedule replaces it, so its stable URL always has something to serve.
	retain := time.Duration(config.C.Render.Retain) * time.Second
	job := &models.RenderJob{
		Inspect:         inspect,
		Draft:           draft,
		Variables:       variables,
		ID:              uuid.New(),
		DashboardID:     dashboardID,
		TokenHash:       tokenHash,
		RenderTokenHash: renderTokenHash,
		Status:          models.RenderStatusPending,
		Width:           width,
		Height:          height,
		Format:          format,
		WebhookURL:      webhookURL,
		ScheduleID:      scheduleID,
		ExpiresAt:       time.Now().Add(retain),
	}
	if scheduleID != nil {
		job.ExpiresAt = time.Now().Add(scheduleRetain)
	}
	if err := models.CreateRenderJob(job); err != nil {
		return nil, "", fmt.Errorf("persist render job: %w", err)
	}

	// Queue it, and fail the row rather than leaving it pending forever if the
	// queue is full.
	r.tokens.Store(job.ID, rawRenderToken)
	if err := r.Submit(job.ID); err != nil {
		r.tokens.Delete(job.ID)
		job.Status = models.RenderStatusFailed
		job.ErrorMsg = err.Error()
		_ = models.SaveRenderJob(job)
		return nil, "", err
	}

	return job, rawToken, nil
}

// CreateJobSync queues a render and waits for it, for the URL that answers
// with the image itself.
func (r *Render) CreateJobSync(dashboardID uuid.UUID, width, height int, format string, variables map[string]string) (*models.RenderJob, error) {
	job, _, err := r.CreateJob(dashboardID, width, height, format, "", nil, variables)
	if err != nil {
		return nil, err
	}

	return r.waitJob(context.Background(), job)
}

// CreatePreview renders a draft or deployed dashboard for an authenticated tool caller.
// The caller must check edit access before requesting a draft.
func (r *Render) CreatePreview(ctx context.Context, dashboardID uuid.UUID, width, height int, draft bool, variables map[string]string) (*models.RenderJob, error) {
	job, _, err := r.createJob(dashboardID, width, height, "jpg", "", nil, draft, false, variables)
	if err != nil {
		return nil, err
	}
	return r.waitJob(ctx, job)
}

// CreateInspection captures browser diagnostics with the same queued preview.
func (r *Render) CreateInspection(ctx context.Context, dashboardID uuid.UUID, width, height int, draft bool, variables map[string]string) (*models.RenderJob, error) {
	job, _, err := r.createJob(dashboardID, width, height, "jpg", "", nil, draft, true, variables)
	if err != nil {
		return nil, err
	}
	result, err := r.waitJob(ctx, job)
	if err != nil {
		settled, readErr := models.FindRenderJob(job.ID)
		if readErr == nil {
			return settled, err
		}
	}
	return result, err
}

func (r *Render) waitJob(ctx context.Context, job *models.RenderJob) (*models.RenderJob, error) {
	ch := make(chan struct{})
	r.waiters.Store(job.ID, ch)
	defer r.waiters.Delete(job.ID)
	timeout := time.NewTimer(time.Duration(config.C.Render.Timeout)*time.Second + 30*time.Second)
	defer timeout.Stop()
	// Read after registering: a fast worker may finish before the waiter exists.
	for {
		settled, err := models.FindRenderJob(job.ID)
		if err != nil {
			return nil, fmt.Errorf("read render job: %w", err)
		}
		switch settled.Status {
		case models.RenderStatusCompleted:
			return settled, nil
		case models.RenderStatusFailed:
			return nil, fmt.Errorf("render failed: %s", settled.ErrorMsg)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("render timed out")
		case <-ch:
			ch = nil
		}
	}
}

// requeuePending resubmits jobs left pending by a restart, each with a fresh
// render token since the raw one was never written down.
func (r *Render) requeuePending() {
	jobs, err := models.ListPendingRenderJobs()
	if err != nil {
		r.log.Warn("failed to load pending render jobs", zap.Error(err))
		return
	}
	for _, job := range jobs {
		if job.ExpiresAt.Before(time.Now()) {
			continue
		}

		rawToken, hash, err := generateToken()
		if err != nil {
			r.log.Warn("failed to mint render token for requeue",
				zap.String("job_id", job.ID.String()), zap.Error(err))
			continue
		}
		job.RenderTokenHash = hash
		if err := models.SaveRenderJob(job); err != nil {
			r.log.Warn("failed to store render token",
				zap.String("job_id", job.ID.String()), zap.Error(err))
			continue
		}

		r.tokens.Store(job.ID, rawToken)
		if err := r.Submit(job.ID); err != nil {
			r.tokens.Delete(job.ID)
			r.log.Warn("failed to requeue render job",
				zap.String("job_id", job.ID.String()), zap.Error(err))
		}
	}
}

func (r *Render) worker(id int) {
	defer r.wg.Done()
	r.log.Debug("render worker started", zap.Int("worker", id))
	for {
		select {
		case <-r.stop:
			r.log.Debug("render worker stopping", zap.Int("worker", id))
			return
		case jobID := <-r.jobs:
			r.runJob(jobID)
		}
	}
}

// runJob loads the queued job and renders it if it is still wanted. A job
// cancelled while it sat in the queue is dropped here.
func (r *Render) runJob(jobID uuid.UUID) {
	defer r.tokens.Delete(jobID)

	job, err := models.FindRenderJob(jobID)
	if err != nil {
		r.log.Error("failed to load render job", zap.String("job_id", jobID.String()), zap.Error(err))
		return
	}
	if job.Status != models.RenderStatusPending {
		r.log.Debug("skipping render job that is no longer pending",
			zap.String("job_id", jobID.String()), zap.String("status", job.Status))
		return
	}

	log := r.log.With(
		zap.String("job_id", job.ID.String()),
		zap.String("dashboard_id", job.DashboardID.String()),
		zap.String("format", job.Format),
	)
	log.Info("rendering dashboard")

	job.Status = models.RenderStatusProcessing
	if err := models.SaveRenderJob(job); err != nil {
		log.Error("failed to mark job as processing", zap.Error(err))
		return
	}

	data, err := r.capture(job)
	if err != nil {
		r.failJob(job, err, log)
		return
	}

	// A job cancelled while the page was open is dropped rather than written:
	// the file would have nothing to belong to.
	if current, err := models.FindRenderJob(job.ID); err == nil && current.Status != models.RenderStatusProcessing {
		log.Info("discarding render that was cancelled while it ran",
			zap.String("status", current.Status))
		return
	}

	outputPath := filepath.Join(config.C.Render.StoragePath, fmt.Sprintf("%s.%s", job.ID, job.Format))
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		r.failJob(job, fmt.Errorf("write render output: %w", err), log)
		return
	}

	job.Status = models.RenderStatusCompleted
	job.ResultPath = outputPath
	if err := models.SaveRenderJob(job); err != nil {
		log.Error("failed to mark job as completed", zap.Error(err))
		return
	}

	// A schedule's URL serves its latest render, so the one it replaces is due
	// for sweeping the moment this one lands.
	if job.ScheduleID != nil {
		if err := models.ExpireSupersededScheduleRenders(*job.ScheduleID, job.ID); err != nil {
			log.Warn("failed to expire the previous scheduled render", zap.Error(err))
		}
	}

	r.notifyWaiter(job.ID)
	if job.WebhookURL != "" {
		go r.callWebhook(job)
	}

	log.Info("render completed", zap.String("output", outputPath))
}

// capture opens the dashboard in the headless browser and returns the encoded
// file. The page is closed before the bytes are written, so a slow disk never
// holds a browser tab open.
func (r *Render) capture(job *models.RenderJob) ([]byte, error) {
	browser, err := r.acquireBrowser()
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(config.C.Render.Timeout) * time.Second
	page, err := browser.Timeout(timeout).Page(proto.TargetCreateTarget{})
	if err != nil {
		// A browser that died between jobs looks exactly like this, so drop it
		// and let the next render launch a fresh one.
		r.closeBrowser()
		return nil, fmt.Errorf("open browser page: %w", err)
	}
	defer func() { _ = page.Close() }()
	var inspection *captureInspection
	if job.Inspect {
		var token string
		if raw, ok := r.tokens.Load(job.ID); ok {
			token = raw.(string)
		}
		inspection = startInspection(page, token)
		defer func() {
			inspection.stop()
			raw, err := json.Marshal(inspection.report())
			if err == nil {
				job.Diagnostics = string(raw)
			}
		}()
	}

	// The viewport is the render size: a dashboard in fit mode lays itself out
	// to whatever it is given, so the window is the composition.
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:  job.Width,
		Height: job.Height,
	}); err != nil {
		return nil, fmt.Errorf("set viewport: %w", err)
	}

	// The viewer reads var-<name> the way a shared link carries a variable.
	query := neturl.Values{}
	if token, ok := r.tokens.Load(job.ID); ok {
		query.Set("token", token.(string))
	}
	if job.Draft {
		query.Set("view", "draft")
	}
	for name, value := range job.Variables {
		query.Set("var-"+name, value)
	}
	url := fmt.Sprintf("%s/dashboard/%s", baseURL(), job.DashboardID)
	if len(query) > 0 {
		url += "?" + query.Encode()
	}
	if err := page.Timeout(timeout).Navigate(url); err != nil {
		return nil, fmt.Errorf("navigate to dashboard: %w", err)
	}
	if err := waitForDashboard(page, timeout); err != nil {
		return nil, err
	}

	if inspection != nil {
		inspection.measure(page)
	}

	if job.Format == "pdf" {
		return renderPDF(page, job)
	}

	// Screenshots are captured as PNG and re-encoded, so every format goes
	// through one code path and only the last step differs.
	shot, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
		Clip: &proto.PageViewport{
			Width:  float64(job.Width),
			Height: float64(job.Height),
			Scale:  1,
		},
		FromSurface:           true,
		CaptureBeyondViewport: false,
	})
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}
	return convertFromPNG(shot, job.Format)
}

// waitForDashboard waits for the page to say it has drawn. The ready flag is
// the dashboard view's own signal that its widgets are mounted; a page that
// never sets it, such as an error page or a build without the flag, still
// renders once the DOM stops moving.
func waitForDashboard(page *rod.Page, timeout time.Duration) error {
	if err := page.Timeout(timeout).WaitLoad(); err != nil {
		return fmt.Errorf("wait for page load: %w", err)
	}
	wait := readyWait
	if timeout < wait {
		wait = timeout
	}
	_, _ = page.Timeout(wait).Element(readyAttr)
	if err := page.Timeout(timeout).WaitStable(500 * time.Millisecond); err != nil {
		return fmt.Errorf("wait for page stability: %w", err)
	}
	return nil
}

// renderPDF prints the page at the requested pixel size, converted to inches
// at the CSS reference of 96 dpi so a PDF frames the same layout an image of
// those dimensions would.
func renderPDF(page *rod.Page, job *models.RenderJob) ([]byte, error) {
	stream, err := page.PDF(&proto.PagePrintToPDF{
		PrintBackground:   true,
		PreferCSSPageSize: true,
		PaperWidth:        ptr(float64(job.Width) / 96.0),
		PaperHeight:       ptr(float64(job.Height) / 96.0),
		MarginTop:         ptr(0.0),
		MarginBottom:      ptr(0.0),
		MarginLeft:        ptr(0.0),
		MarginRight:       ptr(0.0),
	})
	if err != nil {
		return nil, fmt.Errorf("generate PDF: %w", err)
	}
	defer func() { _ = stream.Close() }()

	data, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read PDF data: %w", err)
	}
	return data, nil
}

// acquireBrowser returns the shared browser, launching one on the first render
// and replacing one that has gone away.
func (r *Render) acquireBrowser() (*rod.Browser, error) {
	r.browserMu.Lock()
	defer r.browserMu.Unlock()

	if r.browser != nil {
		if _, err := r.browser.Version(); err == nil {
			return r.browser, nil
		}
		r.log.Warn("headless browser is gone; relaunching")
		r.discardBrowser()
	}

	// Find a browser rather than downloading one. A server that has no Chrome
	// installed should be told to install one, not have 150 MB appear in a
	// cache directory the operator never chose.
	bin := config.C.Render.BrowserPath
	if bin == "" {
		found, ok := launcher.LookPath()
		if !ok {
			return nil, fmt.Errorf("no Chrome or Chromium found; install one or set render.browser_path")
		}
		bin = found
	}

	// rod's leakless helper kills the browser if this process dies without
	// getting to close it. Its watchdog is a glibc binary, so on a musl host,
	// the Alpine image among them, the launch fails outright and the second
	// attempt goes without it. Stop() closes the browser either way; what is
	// lost is only the guarantee after an unclean kill.
	l, browser, err := launchBrowser(bin, true)
	if err != nil {
		r.log.Warn("headless browser did not start with the leak watchdog; retrying without it",
			zap.Error(err))
		l, browser, err = launchBrowser(bin, false)
	}
	if err != nil {
		return nil, err
	}

	r.launcher = l
	r.browser = browser
	r.log.Info("headless browser launched", zap.String("binary", bin))
	return browser, nil
}

// launchBrowser starts one headless browser and connects to it.
func launchBrowser(bin string, leakless bool) (*launcher.Launcher, *rod.Browser, error) {
	l := launcher.New().Bin(bin).Headless(true).Leakless(leakless).
		// Rendering runs as a service account, often in a container: the
		// sandbox needs privileges that are not there, and /dev/shm is
		// routinely too small for a page the size of a wall panel.
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("hide-scrollbars")

	url, err := l.Launch()
	if err != nil {
		l.Cleanup()
		return nil, nil, fmt.Errorf("launch headless browser: %w", err)
	}

	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		l.Kill()
		l.Cleanup()
		return nil, nil, fmt.Errorf("connect to headless browser: %w", err)
	}
	return l, browser, nil
}

// closeBrowser shuts the browser down if one is running.
func (r *Render) closeBrowser() {
	r.browserMu.Lock()
	defer r.browserMu.Unlock()
	r.discardBrowser()
}

// discardBrowser releases the browser and its process. The caller holds the
// browser lock.
func (r *Render) discardBrowser() {
	if r.browser != nil {
		if err := r.browser.Close(); err != nil {
			r.log.Debug("error closing browser", zap.Error(err))
		}
		r.browser = nil
	}
	if r.launcher != nil {
		r.launcher.Kill()
		r.launcher.Cleanup()
		r.launcher = nil
	}
}

// sweeper deletes expired renders and settles jobs a crash left in progress.
func (r *Render) sweeper() {
	defer r.wg.Done()
	r.sweep()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *Render) sweep() {
	// A job still marked processing long after any render could have finished
	// belongs to a process that is gone. Failing it releases the render token
	// its browser was holding.
	if n, err := models.FailStalledRenderJobs(); err != nil {
		r.log.Warn("failed to settle stalled render jobs", zap.Error(err))
	} else if n > 0 {
		r.log.Warn("settled render jobs left in progress by a restart", zap.Int64("jobs", n))
	}

	jobs, err := models.ExpiredRenderJobs()
	if err != nil {
		r.log.Warn("failed to list expired render jobs", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(jobs))
	for _, job := range jobs {
		if job.ResultPath != "" {
			if err := os.Remove(job.ResultPath); err != nil && !os.IsNotExist(err) {
				r.log.Warn("failed to remove render output",
					zap.String("path", job.ResultPath), zap.Error(err))
			}
		}
		ids = append(ids, job.ID)
	}
	if err := models.DeleteRenderJobs(ids); err != nil {
		r.log.Warn("failed to delete expired render jobs", zap.Error(err))
		return
	}
	r.log.Debug("swept expired renders", zap.Int("jobs", len(ids)))
}

// notifyWaiter releases whatever is waiting on a settled job. A settled job's
// token no longer grants a view, so the socket its browser opened is
// rechecked and closed now rather than at the hub's next interval.
func (r *Render) notifyWaiter(jobID uuid.UUID) {
	if ch, ok := r.waiters.LoadAndDelete(jobID); ok {
		close(ch.(chan struct{}))
	}
	mq.WS.Recheck()
}

// failJob records why a render stopped and releases anything waiting on it.
func (r *Render) failJob(job *models.RenderJob, reason error, log *zap.Logger) {
	log.Error("render failed", zap.Error(reason))
	job.Status = models.RenderStatusFailed
	job.ErrorMsg = reason.Error()
	if err := models.SaveRenderJob(job); err != nil {
		log.Error("failed to mark job as failed", zap.Error(err))
	}

	r.notifyWaiter(job.ID)
	if job.WebhookURL != "" {
		go r.callWebhook(job)
	}
}

// callWebhook reports a settled job to the URL the requester named. The
// notification carries a URL rather than the file: the result is already
// addressable, and a caller that wanted bytes would have waited for them.
func (r *Render) callWebhook(job *models.RenderJob) {
	var payload map[string]string
	if job.Status == models.RenderStatusCompleted {
		payload = map[string]string{
			"status":       "completed",
			"job_id":       job.ID.String(),
			"dashboard_id": job.DashboardID.String(),
			"url": fmt.Sprintf("%s/dashboard/%s/render/job/%s.%s",
				baseURL(), job.DashboardID, job.ID, job.Format),
		}
	} else {
		payload = map[string]string{
			"status":       "failed",
			"job_id":       job.ID.String(),
			"dashboard_id": job.DashboardID.String(),
			"message":      job.ErrorMsg,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		r.log.Error("failed to encode webhook payload", zap.Error(err))
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(job.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		r.log.Warn("render webhook failed", zap.String("url", job.WebhookURL), zap.Error(err))
		return
	}
	_ = resp.Body.Close()
	r.log.Debug("render webhook delivered",
		zap.String("url", job.WebhookURL), zap.Int("status", resp.StatusCode))
}

// generateToken returns a random token and the digest stored for it.
func generateToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	raw = hex.EncodeToString(b)
	return raw, models.HashRenderToken(raw), nil
}

// baseURL is the address the headless browser dials this server on. It is the
// server's own base URL rather than a setting of its own: the browser runs
// beside the server and reaches it the same way anything else does.
func baseURL() string {
	return config.C.Server.BaseURL
}

// scheduleRetain is how long a scheduled render is kept when nothing replaces
// it. A schedule's URL must keep answering between fires, so the window has to
// outlast any sane interval; the next completed render expires the one before
// it, so this only ever applies to a schedule that has stopped firing.
const scheduleRetain = 30 * 24 * time.Hour

// convertFromPNG re-encodes a captured screenshot into the requested format.
func convertFromPNG(pngData []byte, format string) ([]byte, error) {
	if format == "png" {
		return pngData, nil
	}

	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("decode PNG: %w", err)
	}

	var buf bytes.Buffer
	switch format {
	case "jpg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	case "gif":
		err = gif.Encode(&buf, img, nil)
	case "bmp":
		err = bmp.Encode(&buf, img)
	case "tiff":
		err = tiff.Encode(&buf, img, nil)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", format, err)
	}
	return buf.Bytes(), nil
}

// Formats lists the output formats a render may be asked for.
var Formats = []string{"png", "jpg", "gif", "bmp", "tiff", "pdf"}

// ValidFormat reports whether a format is one this build can produce.
func ValidFormat(format string) bool {
	for _, f := range Formats {
		if f == format {
			return true
		}
	}
	return false
}

// ContentTypeForFormat returns the MIME type a rendered file is served as.
func ContentTypeForFormat(format string) string {
	switch format {
	case "pdf":
		return "application/pdf"
	case "jpg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "tiff":
		return "image/tiff"
	default:
		return "image/png"
	}
}

func ptr[T any](v T) *T {
	return &v
}
