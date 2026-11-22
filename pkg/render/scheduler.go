package render

import (
	"fmt"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// S is the package-level render scheduler handlers reload after a change.
var S *Scheduler

// Scheduler fires stored render schedules. It runs on the same cron dialect
// the schedule node does (pkg/cronspec), so an expression the flow editor
// explains means the same thing here.
type Scheduler struct {
	log  *zap.Logger
	mu   sync.Mutex
	cron *cron.Cron
}

// InitScheduler creates the scheduler and sets S.
func InitScheduler(log *zap.Logger) {
	S = &Scheduler{log: log}
}

// Start arms every enabled schedule.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cron = cron.New(cron.WithParser(cronspec.Parser))
	s.load()
	s.cron.Start()
	s.log.Info("render scheduler started")
	return nil
}

// Stop halts the runner and waits for a firing render to be queued.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
	s.log.Info("render scheduler stopped")
}

// Reload rebuilds the entries from the database, so a schedule saved in the UI
// takes effect without a restart.
func (s *Scheduler) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cron == nil {
		return
	}
	for _, entry := range s.cron.Entries() {
		s.cron.Remove(entry.ID)
	}
	s.load()
}

// load arms the enabled schedules. A schedule whose expression no longer
// parses is skipped and named in the log rather than stopping the rest.
func (s *Scheduler) load() {
	schedules, err := models.ListEnabledRenderSchedules()
	if err != nil {
		s.log.Warn("failed to list render schedules", zap.Error(err))
		return
	}

	armed := 0
	for _, sched := range schedules {
		if err := s.add(sched); err != nil {
			s.log.Warn("skipping invalid render schedule",
				zap.Uint("schedule_id", sched.ID),
				zap.String("cron", sched.CronExpr),
				zap.Error(err))
			continue
		}
		armed++
	}
	s.log.Info("armed render schedules", zap.Int("count", armed))
}

func (s *Scheduler) add(sched *models.RenderSchedule) error {
	// Copy what the entry needs, so a later edit of the row cannot change what
	// an already-armed entry renders.
	id, dashboardID := sched.ID, sched.DashboardID
	width, height, format, webhook := sched.Width, sched.Height, sched.Format, sched.WebhookURL

	_, err := s.cron.AddFunc(cronspec.Spec(sched.CronExpr, sched.Location), func() {
		s.log.Info("scheduled render fired",
			zap.Uint("schedule_id", id),
			zap.String("dashboard_id", dashboardID.String()))

		if _, _, err := R.CreateJob(dashboardID, width, height, format, webhook, &id, nil); err != nil {
			s.log.Error("failed to queue scheduled render",
				zap.Uint("schedule_id", id), zap.Error(err))
		}
	})
	if err != nil {
		return fmt.Errorf("arm cron entry: %w", err)
	}
	return nil
}
