package models

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Render job statuses. A job is pending until a worker picks it up, processing
// while the headless browser holds the page, and then either completed with a
// file on disk or failed with the reason it stopped.
const (
	RenderStatusPending    = "pending"
	RenderStatusProcessing = "processing"
	RenderStatusCompleted  = "completed"
	RenderStatusFailed     = "failed"
)

// RenderJob is one request to photograph a dashboard. It is a row rather than
// a function call because the work outlives the request that asked for it: a
// schedule fires with nobody watching, a webhook is delivered afterwards, and
// the result is fetched by URL later.
//
// Two tokens hang off a job and they point in opposite directions. TokenHash
// is what the requester later presents to collect the result. RenderTokenHash
// is what the headless browser presents on the way in. That one is a viewer
// grant for this dashboard and nothing else, which is why a private dashboard
// can be rendered without handing the browser an account.
type RenderJob struct {
	Inspect     bool   `gorm:"not null;default:false" json:"-"`
	Diagnostics string `gorm:"type:text" json:"-"`
	Draft       bool   `gorm:"not null;default:false" json:"draft" description:"Whether this job photographs the editable draft."`
	// Variables are the dashboard variable values the render opens with, already
	// resolved to option values.
	Variables       map[string]string `gorm:"serializer:json;type:json" json:"variables,omitempty" description:"Dashboard variable values the render uses."`
	ID              uuid.UUID         `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	DashboardID     uuid.UUID         `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Target dashboard ID."`
	Dashboard       Dashboard         `gorm:"foreignKey:DashboardID" json:"-"`
	TokenHash       string            `gorm:"not null;index" json:"-"`
	RenderTokenHash string            `gorm:"index" json:"-"`
	Status          string            `gorm:"not null" json:"status" description:"Job status: pending, processing, completed, or failed."`
	ResultPath      string            `json:"result_path,omitempty" description:"Path to the rendered file on the server."`
	Width           int               `gorm:"not null;default:1920" json:"width" description:"Render width in pixels."`
	Height          int               `gorm:"not null;default:1080" json:"height" description:"Render height in pixels."`
	Format          string            `gorm:"not null;default:png" json:"format" description:"Output format: png, pdf, jpg, gif, bmp, or tiff."`
	ErrorMsg        string            `json:"error_message,omitempty" description:"Why the job failed, when it did."`
	WebhookURL      string            `json:"webhook_url,omitempty" description:"URL notified when the job settles."`
	ScheduleID      *uint             `gorm:"index" json:"schedule_id,omitempty" description:"Parent schedule ID when the job was scheduled."`
	ExpiresAt       time.Time         `gorm:"not null;index" json:"expires_at" description:"When the job and its file are swept."`
	CreatedAt       time.Time         `json:"created_at" description:"Creation timestamp."`
	UpdatedAt       time.Time         `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID if one has not been set.
func (rj *RenderJob) BeforeCreate(tx *gorm.DB) error {
	if rj.ID == uuid.Nil {
		rj.ID = uuid.New()
	}
	return nil
}

// RenderGrantWindow bounds how long a job's render token is honoured. A render
// that has not finished within it belongs to a browser that is gone, and the
// sweeper settles the row; until it runs, this is what stops a crashed job's
// token from staying a valid viewer grant.
const RenderGrantWindow = 5 * time.Minute

// HashRenderToken digests a render token for storage. Unlike a password, a
// render token is 256 bits from a cryptographic source and lives for minutes,
// so a salted slow hash buys nothing while costing a table scan on every
// lookup, and the headless browser presents its token on every request the page
// makes. A digest is an indexed lookup instead.
func HashRenderToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CheckRenderToken compares a presented token against a stored digest.
func CheckRenderToken(stored, raw string) bool {
	return subtle.ConstantTimeCompare([]byte(stored), []byte(HashRenderToken(raw))) == 1
}

// CreateRenderJob persists a new render job.
func CreateRenderJob(job *RenderJob) error {
	return db.DB.Create(job).Error
}

// SaveRenderJob writes a job's current state back.
func SaveRenderJob(job *RenderJob) error {
	return db.DB.Save(job).Error
}

// FindRenderJob returns one render job by ID.
func FindRenderJob(id uuid.UUID) (*RenderJob, error) {
	var job RenderJob
	err := db.DB.Where("id = ?", id).First(&job).Error
	return &job, err
}

// ListRenderJobsByDashboardID returns a dashboard's jobs, newest first.
func ListRenderJobsByDashboardID(dashboardID uuid.UUID) ([]*RenderJob, error) {
	var jobs []*RenderJob
	err := db.DB.Where("dashboard_id = ?", dashboardID).Order("created_at DESC").Find(&jobs).Error
	return jobs, err
}

// ListPendingRenderJobs returns unstarted jobs oldest first, for requeueing
// after a restart.
func ListPendingRenderJobs() ([]*RenderJob, error) {
	var jobs []*RenderJob
	err := db.DB.Where("status = ?", RenderStatusPending).Order("created_at ASC").Find(&jobs).Error
	return jobs, err
}

// FindRenderJobByRenderToken resolves the credential the headless browser
// carries into the job it belongs to. Only a job still being worked on is
// considered, so the grant lasts exactly as long as the render does.
func FindRenderJobByRenderToken(rawToken string) (*RenderJob, error) {
	if rawToken == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var job RenderJob
	err := db.DB.Where("render_token_hash = ?", HashRenderToken(rawToken)).
		Where("status IN ?", []string{RenderStatusPending, RenderStatusProcessing}).
		Where("updated_at > ?", time.Now().Add(-RenderGrantWindow)).
		First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// FailStalledRenderJobs settles jobs left in progress by a crash or a restart,
// and reports how many it moved. Their browsers are gone, so leaving them
// would leave their view grants standing as well.
func FailStalledRenderJobs() (int64, error) {
	res := db.DB.Model(&RenderJob{}).
		Where("status = ? AND updated_at < ?", RenderStatusProcessing, time.Now().Add(-RenderGrantWindow)).
		Updates(map[string]any{
			"status":    RenderStatusFailed,
			"error_msg": "render did not finish; the server restarted or the browser stopped",
		})
	return res.RowsAffected, res.Error
}

// ExpireSupersededScheduleRenders marks a schedule's older renders for
// sweeping once a newer one has landed. A schedule serves its latest result
// from one URL, so what came before it is no longer reachable. Renders
// created after keep are left alone: one may already be queued or running
// when a render outlasts the schedule's interval, and it is the newer result.
func ExpireSupersededScheduleRenders(scheduleID uint, keep uuid.UUID) error {
	kept, err := FindRenderJob(keep)
	if err != nil {
		return err
	}
	return db.DB.Model(&RenderJob{}).
		Where("schedule_id = ? AND id != ? AND created_at <= ?", scheduleID, keep, kept.CreatedAt).
		Update("expires_at", time.Now()).Error
}

// FindLatestCompletedRenderJobBySchedule returns the newest finished render a
// schedule produced, which is what its stable result URL serves.
func FindLatestCompletedRenderJobBySchedule(scheduleID uint) (*RenderJob, error) {
	var job RenderJob
	err := db.DB.Where("schedule_id = ? AND status = ?", scheduleID, RenderStatusCompleted).
		Order("created_at DESC").First(&job).Error
	return &job, err
}

// ExpiredRenderJobs returns jobs past their expiry, so the sweeper can delete
// their files before dropping the rows.
func ExpiredRenderJobs() ([]*RenderJob, error) {
	var jobs []*RenderJob
	err := db.DB.Where("expires_at < ?", time.Now()).Find(&jobs).Error
	return jobs, err
}

// DeleteRenderJobs removes the given jobs by ID.
func DeleteRenderJobs(ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	return db.DB.Where("id IN ?", ids).Delete(&RenderJob{}).Error
}

// RenderSchedule renders a dashboard on a cron schedule. The result is served
// from one stable URL per schedule, so a photo frame or an e-ink panel points
// at an address that never changes and always answers with the latest render.
type RenderSchedule struct {
	ID          uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Target dashboard ID."`
	Dashboard   Dashboard `gorm:"foreignKey:DashboardID" json:"-"`
	Name        string    `json:"name" description:"Display name for the schedule."`
	CronExpr    string    `gorm:"not null" json:"cron_expr" description:"Cron expression the render fires on."`
	Location    string    `json:"location" description:"IANA time zone the expression is evaluated in; empty means server local time."`
	Format      string    `gorm:"not null;default:png" json:"format" description:"Output format."`
	Width       int       `gorm:"not null;default:1920" json:"width" description:"Render width in pixels."`
	Height      int       `gorm:"not null;default:1080" json:"height" description:"Render height in pixels."`
	Enabled     bool      `gorm:"default:true" json:"enabled" description:"Whether the schedule fires."`
	WebhookURL  string    `json:"webhook_url,omitempty" description:"URL notified when a scheduled render settles."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// FindRenderScheduleByID returns a render schedule with its dashboard preloaded.
func FindRenderScheduleByID(id uint) (*RenderSchedule, error) {
	var schedule RenderSchedule
	err := db.DB.Preload("Dashboard").First(&schedule, id).Error
	return &schedule, err
}

// ListEnabledRenderSchedules returns the schedules the cron runner arms.
func ListEnabledRenderSchedules() ([]*RenderSchedule, error) {
	var schedules []*RenderSchedule
	err := db.DB.Where("enabled = ?", true).Find(&schedules).Error
	return schedules, err
}

// ListRenderSchedulesByDashboardID returns a dashboard's schedules, newest first.
func ListRenderSchedulesByDashboardID(dashboardID uuid.UUID) ([]*RenderSchedule, error) {
	var schedules []*RenderSchedule
	err := db.DB.Where("dashboard_id = ?", dashboardID).Order("created_at DESC").Find(&schedules).Error
	return schedules, err
}

// CreateRenderSchedule persists a new render schedule.
func CreateRenderSchedule(schedule *RenderSchedule) error {
	return db.DB.Create(schedule).Error
}

// SaveRenderSchedule writes a schedule's current state back.
func SaveRenderSchedule(schedule *RenderSchedule) error {
	return db.DB.Save(schedule).Error
}

// DeleteRenderSchedule removes a schedule. The renders it produced stay until
// they expire, so a stable URL keeps answering while a display is repointed.
func DeleteRenderSchedule(id uint) error {
	return db.DB.Delete(&RenderSchedule{}, id).Error
}
