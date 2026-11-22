package mcpserver

import (
	"context"
	"errors"

	"github.com/grmrgecko/karakuri/pkg/backup"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createBackupInput struct {
	Passphrase string `json:"passphrase,omitempty" jsonschema:"Passphrase for this archive, at least 12 characters; omit to use the stored one."`
}

type backupNameInput struct {
	Name       string `json:"name" jsonschema:"Archive file name from list_backups."`
	Passphrase string `json:"passphrase,omitempty" jsonschema:"Passphrase to verify with; omit to use the stored one."`
}

type saveBackupScheduleInput struct {
	Enabled    bool    `json:"enabled" jsonschema:"Whether scheduled backups run."`
	Cron       string  `json:"cron,omitempty" jsonschema:"Cron schedule in the server's time zone; omit for 0 3 * * *."`
	Directory  string  `json:"directory,omitempty" jsonschema:"Absolute directory for archives; omit for backups under the data directory."`
	Retain     int     `json:"retain,omitempty" jsonschema:"Archives to keep, 1 to 365; omit for 14."`
	Passphrase *string `json:"passphrase,omitempty" jsonschema:"New stored passphrase of at least 12 characters; omit to keep the stored one, empty to clear it."`
}

func backupSvc() (*backup.Service, error) {
	if backup.S == nil {
		return nil, errors.New("the backup service is not running")
	}
	return backup.S, nil
}

// addBackupTools registers the backup tools. There is no download or
// restore: an archive holds every credential, and restore needs the server
// stopped.
func addBackupTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_backups",
		Description: "List backup archives in the backup directory, newest first, with size, verification, and record counts, plus any backup running now and the most recent runs."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsRead); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			list, err := svc.ListArchives()
			if err != nil {
				return nil, nil, opErr(err)
			}
			runs, err := models.ListBackupRuns(10)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"directory": list.Directory, "archives": list.Archives, "running": list.Running, "runs": runs}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "create_backup",
		Description: "Start an encrypted backup of the whole installation into the backup directory. Answers the run at once; list_backups shows the result."},
		func(ctx context.Context, req *mcp.CallToolRequest, in createBackupInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsWrite); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			id := callerID(ctx)
			r, err := svc.Stored(backup.Request{Trigger: models.BackupTriggerManual, Passphrase: in.Passphrase, UserID: &id})
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage(err.Error()))
			}
			run, err := svc.Start(r)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage(err.Error()))
			}
			return nil, map[string]any{"run": run}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "verify_backup",
		Description: "Read a whole archive and check every part against its manifest. Answers the manifest: version, time, database engine, counts, plugins, and what was left out."},
		func(ctx context.Context, req *mcp.CallToolRequest, in backupNameInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsWrite); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			m, err := svc.VerifyArchive(in.Name, in.Passphrase)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"ok": true, "manifest": m}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_backup",
		Description: "Delete an archive from the backup directory. Its run record stays."},
		func(ctx context.Context, req *mcp.CallToolRequest, in backupNameInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsWrite); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			if err := svc.DeleteArchive(in.Name); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"deleted": in.Name}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_backup_schedule",
		Description: "Read the backup schedule: enabled, cron and its description, directory, retention, whether a passphrase is stored, the next time, and the last run."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsRead); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			v, err := svc.View()
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"schedule": v}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "save_backup_schedule",
		Description: "Change the backup schedule. The directory is created and test-written now; enabling needs a stored passphrase. A lost passphrase cannot be recovered."},
		func(ctx context.Context, req *mcp.CallToolRequest, in saveBackupScheduleInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermBackupsWrite); err != nil {
				return nil, nil, err
			}
			svc, err := backupSvc()
			if err != nil {
				return nil, nil, err
			}
			v, err := svc.SaveView(backup.ScheduleUpdate{Enabled: in.Enabled, Cron: in.Cron, Directory: in.Directory, Retain: in.Retain, Passphrase: in.Passphrase})
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"schedule": v}, nil
		})
}
