package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/backup"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// BackupCreateNode writes a backup of the installation with the schedule's
// stored passphrase and waits for it.
type BackupCreateNode struct{}

func (BackupCreateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "backup-create", Label: "Back up", Folder: flow.FolderUtility,
		Icon:        "las la-archive",
		Description: "Writes an encrypted backup of the whole installation and waits for it to verify",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "Start", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{{ID: "out", Label: "Archive", Type: flow.PortObject, PayloadSchema: map[string]any{"type": "object", "properties": map[string]any{
			"archive": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
			"size": map[string]any{"type": "integer"}, "duration_ms": map[string]any{"type": "integer"},
			"counts": map[string]any{"type": "object"}, "verified": map[string]any{"type": "string"},
		}}}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "directory", Label: "Directory", Type: flow.FieldString, Help: "Absolute directory for the archive. Empty uses the schedule's directory. Retention applies only to scheduled backups."},
		},
	}
}

type backupCreateConfig struct {
	Directory string `json:"directory"`
}

// Execute fails without retry when no passphrase is stored or the service is
// not running, since waiting will not fix either; a failed write, such as a
// full disk, may succeed on a later attempt.
func (BackupCreateNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg backupCreateConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if backup.S == nil {
		return nil, &engine.ExecutionError{Message: "the backup service is not running"}
	}
	req := backup.Request{Trigger: models.BackupTriggerFlow, Directory: strings.TrimSpace(cfg.Directory)}
	if id, err := uuid.Parse(flowOf(ec)); err == nil {
		req.FlowID = &id
	}
	req, err := backup.S.Stored(req)
	if err != nil {
		return nil, &engine.ExecutionError{Message: err.Error()}
	}
	run, err := backup.S.Create(ctx, req)
	if err != nil {
		if run == nil {
			return nil, &engine.ExecutionError{Message: err.Error()}
		}
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	took := int64(0)
	if run.FinishedAt != nil {
		took = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
	}
	return engine.One("out", &engine.Msg{Payload: map[string]any{
		"archive": run.Archive, "path": run.Directory + "/" + run.Archive, "size": run.Size,
		"duration_ms": took, "counts": run.Counts, "verified": run.Verified,
	}}), nil
}

// BackupResultNode starts a run when a backup finishes.
type BackupResultNode struct{}

func (BackupResultNode) Meta() engine.NodeMeta {
	props := map[string]any{}
	for _, f := range []string{"status", "trigger", "archive", "error", "started_at", "finished_at"} {
		props[f] = map[string]any{"type": "string"}
	}
	props["size"] = map[string]any{"type": "integer"}
	return engine.NodeMeta{
		Type: "backup-result", Label: "Backup finished", Folder: flow.FolderUtility,
		Category: flow.CategoryTrigger, Icon: "las la-archive", TestWithoutParams: true,
		Description: "Starts a run when a backup succeeds or fails, such as to send a notification",
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Result", Type: flow.PortObject, PayloadSchema: map[string]any{"type": "object", "properties": props}}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "on", Label: "On", Type: flow.FieldString, Default: "failed", Options: []engine.Option{
				{Value: "failed", Label: "Failure"}, {Value: "succeeded", Label: "Success"}, {Value: "any", Label: "Either"},
			}, Help: "Which outcomes start a run. Backups that finished before the flow was deployed are not replayed."},
		},
	}
}

type backupResultConfig struct {
	On string `json:"on"`
}

func (cfg backupResultConfig) matches(status string) (bool, error) {
	switch cfg.On {
	case "", "failed":
		return status == models.BackupFailed, nil
	case "succeeded":
		return status == models.BackupSucceeded, nil
	case "any":
		return true, nil
	}
	return false, fmt.Errorf("unknown backup outcome %q", cfg.On)
}

func backupResultMessage(run models.BackupRun) *engine.Msg {
	payload := map[string]any{
		"status": run.Status, "trigger": run.Trigger, "archive": run.Archive, "size": run.Size,
		"error": run.Error, "started_at": run.StartedAt.Format(time.RFC3339Nano), "finished_at": "",
	}
	if run.FinishedAt != nil {
		payload["finished_at"] = run.FinishedAt.Format(time.RFC3339Nano)
	}
	return &engine.Msg{Topic: "backup", Payload: payload}
}

// Execute forwards a trigger sample, or describes the latest finished backup
// for a test.
func (BackupResultNode) Execute(_ context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg backupResultConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if _, err := cfg.matches(models.BackupFailed); err != nil {
		return nil, err
	}
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	runs, err := models.ListBackupRuns(1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, errors.New("no backup has run yet")
	}
	return engine.One("out", backupResultMessage(*runs[0])), nil
}

// Start emits each finished backup that matches until disarmed.
func (BackupResultNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg backupResultConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if _, err := cfg.matches(models.BackupFailed); err != nil {
		return err
	}
	if backup.S == nil {
		return errors.New("the backup service is not running")
	}
	finished := make(chan models.BackupRun, 16)
	stop := backup.S.Watch(func(run models.BackupRun) {
		select {
		case finished <- run:
		default:
		}
	})
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case run := <-finished:
			if ok, _ := cfg.matches(run.Status); ok {
				emit(backupResultMessage(run))
			}
		}
	}
}
