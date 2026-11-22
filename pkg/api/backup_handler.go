package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/backup"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// BackupScheduleInput changes the schedule.
type BackupScheduleInput struct {
	Enabled    bool    `json:"enabled" description:"Whether scheduled backups run."`
	Cron       string  `json:"cron" description:"Cron schedule; empty is 0 3 * * *."`
	Directory  string  `json:"directory" description:"Absolute directory; empty is backups under the data directory."`
	Retain     int     `json:"retain" minimum:"0" maximum:"365" description:"Archives to keep, 1 to 365; 0 is 14."`
	Passphrase *string `json:"passphrase,omitempty" description:"New passphrase of at least 12 characters; omitted keeps the stored one, empty clears it."`
}

// BackupCreateInput starts a backup.
type BackupCreateInput struct {
	Passphrase string `json:"passphrase,omitempty" description:"Passphrase for this archive; empty uses the stored one."`
}

// BackupNameInput names one archive.
type BackupNameInput struct {
	Name       string `path:"name" description:"Archive file name."`
	Passphrase string `json:"passphrase,omitempty" description:"Passphrase to verify with; empty uses the stored one."`
}

// BackupRunsInput limits the run list.
type BackupRunsInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"500" description:"Most recent runs to return; default 50."`
}

// backupService answers the running service or an error a client can read.
func backupService() (*backup.Service, error) {
	if backup.S == nil {
		return nil, apperrors.ErrServiceUnavailable.WithMessage("the backup service is not running")
	}
	return backup.S, nil
}

// backupErr maps service errors onto the API's.
func backupErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, backup.ErrNoArchive):
		return apperrors.ErrNotFound.WithMessage("no such archive")
	default:
		return apperrors.ErrValidation.WithMessage(err.Error())
	}
}

func backupDoc(u documented, title, desc string) usecase.Interactor {
	u.SetTitle(title)
	u.SetDescription(desc)
	u.SetTags("Backups")
	return u
}

func backupList() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[backup.Listing]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		out.Data, err = svc.ListArchives()
		return backupErr(err)
	})
	return backupDoc(&u, "List Backups", "Lists the archives in the backup directory, newest first, with the backup running now.")
}

func backupRuns() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in BackupRunsInput, out *apiutil.Success[[]*models.BackupRun]) error {
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		runs, err := models.ListBackupRuns(limit)
		out.Data = runs
		return err
	})
	return backupDoc(&u, "List Backup Runs", "Lists recent backups of every trigger, newest first, with their results.")
}

func backupCreate() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in BackupCreateInput, out *apiutil.Success[*models.BackupRun]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		req := backup.Request{Trigger: models.BackupTriggerManual, Passphrase: in.Passphrase}
		if uc := middleware.GetUser(ctx); uc != nil {
			id := uc.UserID
			req.UserID = &id
		}
		req, err = svc.Stored(req)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		run, err := svc.Start(req)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		out.Data = run
		out.Message = "Backup started"
		return nil
	})
	return backupDoc(&u, "Create Backup", "Starts a backup of the whole installation. A backup already running delays it. Answers the run, whose result the run list reports.")
}

func backupVerify() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in BackupNameInput, out *apiutil.Success[*backup.Manifest]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		out.Data, err = svc.VerifyArchive(in.Name, in.Passphrase)
		if err != nil {
			return backupErr(err)
		}
		out.Message = "The archive is complete and every part matches"
		return nil
	})
	return backupDoc(&u, "Verify Backup", "Reads a whole archive and checks every part against its manifest, answering the manifest.")
}

func backupDelete() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in BackupNameInput, out *apiutil.Success[struct{}]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		if err := svc.DeleteArchive(in.Name); err != nil {
			return backupErr(err)
		}
		out.Message = "Archive deleted"
		return nil
	})
	return backupDoc(&u, "Delete Backup", "Removes an archive from the backup directory. Its run record stays.")
}

func backupGetSchedule() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[backup.ScheduleView]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		out.Data, err = svc.View()
		return backupErr(err)
	})
	return backupDoc(&u, "Get Backup Schedule", "Reads the backup schedule. The passphrase is never returned; passphrase_set says whether one is stored.")
}

func backupSaveSchedule() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in BackupScheduleInput, out *apiutil.Success[backup.ScheduleView]) error {
		svc, err := backupService()
		if err != nil {
			return err
		}
		out.Data, err = svc.SaveView(backup.ScheduleUpdate{
			Enabled: in.Enabled, Cron: in.Cron, Directory: in.Directory, Retain: in.Retain, Passphrase: in.Passphrase,
		})
		return backupErr(err)
	})
	return backupDoc(&u, "Save Backup Schedule", "Changes the backup schedule. The directory is created and test-written now, so an unwritable one is refused here rather than at the scheduled time.")
}

// BackupDownloadHandler streams an archive. It is a plain handler because the
// body is the file itself; the archive is encrypted, but with its passphrase
// it holds every credential, so each download is logged with its user.
func BackupDownloadHandler(w http.ResponseWriter, r *http.Request) {
	svc, err := backupService()
	if err != nil {
		writeAppError(w, err)
		return
	}
	name := chi.URLParam(r, "name")
	path, err := svc.Path(name)
	if err != nil {
		writeAppError(w, backupErr(err))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound.WithMessage("no such archive"))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithError(err))
		return
	}
	svc.LogDownload(r.Context(), name)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// RegisterBackupRoutes adds the backup routes to the API service.
func RegisterBackupRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	read := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermBackupsRead), bearerDoc)
	write := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermBackupsWrite), bearerDoc)
	read.Method(http.MethodGet, "/backups", nethttp.NewHandler(backupList()))
	read.Method(http.MethodGet, "/backups/runs", nethttp.NewHandler(backupRuns()))
	read.Method(http.MethodGet, "/backups/schedule", nethttp.NewHandler(backupGetSchedule()))
	write.Method(http.MethodPut, "/backups/schedule", nethttp.NewHandler(backupSaveSchedule()))
	write.Method(http.MethodPost, "/backups", nethttp.NewHandler(backupCreate(), nethttp.SuccessStatus(http.StatusAccepted)))
	write.Method(http.MethodPost, "/backups/{name}/verify", nethttp.NewHandler(backupVerify()))
	write.Method(http.MethodDelete, "/backups/{name}", nethttp.NewHandler(backupDelete()))
}
