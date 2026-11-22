package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/usecase"
)

// streamBoundary is the part boundary of streams relayed to viewers.
const streamBoundary = "karakuriframe"

// nvrVisibleCameras returns the cameras a caller may see on an NVR
// connection: all for connection read, otherwise what the viewer's dashboard
// binds, with all true when it binds every camera.
func nvrVisibleCameras(ctx context.Context, connID string) (map[string]bool, bool, error) {
	if canReadConnection(ctx, connID) {
		return nil, true, nil
	}
	scope := dashscope.FromContext(ctx)
	if scope == nil {
		return nil, false, unauthorizedOrForbidden(ctx)
	}
	cams, all := scope.Cameras(canonicalConnID(connID))
	if !all && len(cams) == 0 {
		return nil, false, apperrors.ErrForbidden.WithMessage("this dashboard shows no camera on that connection")
	}
	return cams, all, nil
}

// nvrCameraDriver authorizes a read of one camera and returns the recorder.
func nvrCameraDriver(ctx context.Context, connID, camera string) (nvr.NVR, error) {
	if !frigate.CameraNameRe.MatchString(camera) {
		return nil, apperrors.ErrValidation.WithMessage("invalid camera name")
	}
	cams, all, err := nvrVisibleCameras(ctx, connID)
	if err != nil {
		return nil, err
	}
	if !all && !cams[camera] {
		return nil, apperrors.ErrForbidden.WithMessage("this dashboard does not show camera " + camera)
	}
	return connops.NVRDriver(connID)
}

// nvrError maps a recorder failure onto an API error.
func nvrError(err error) error {
	var notReceiving *nvr.NotReceivingError
	var unknown *nvr.UnknownCameraError
	var missing *frigate.MediaNotFoundError
	switch {
	case errors.As(err, &notReceiving):
		return apperrors.ErrServiceUnavailable.WithMessage(err.Error())
	case errors.As(err, &unknown), errors.As(err, &missing):
		return apperrors.ErrNotFound.WithMessage(err.Error())
	}
	var app *apperrors.AppError
	if errors.As(err, &app) {
		return app
	}
	return apperrors.ErrBadGateway.WithMessage(err.Error())
}

// queryInt reads a bounded integer query parameter, 0 when absent.
func queryInt(r *http.Request, key string, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

// imageOptions reads height, fps, and bbox from a request.
func imageOptions(r *http.Request) nvr.ImageOptions {
	bbox := r.URL.Query().Get("bbox")
	return nvr.ImageOptions{Height: queryInt(r, "height", 2160), FPS: queryInt(r, "fps", 10),
		BBox: bbox == "1" || bbox == "true"}
}

// copyMedia relays a recorder response to the viewer.
func copyMedia(w http.ResponseWriter, resp *http.Response, fallbackType string) {
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = fallbackType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// NVRSnapshotHandler relays a bound camera's latest frame.
func NVRSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	camera := chi.URLParam(r, "camera")
	recorder, err := nvrCameraDriver(r.Context(), chi.URLParam(r, "id"), camera)
	if err != nil {
		writeAppError(w, err)
		return
	}
	resp, err := recorder.Snapshot(r.Context(), camera, imageOptions(r))
	if err != nil {
		writeAppError(w, nvrError(err))
		return
	}
	copyMedia(w, resp, "image/jpeg")
}

// NVRStreamHandler relays a bound camera's live stream from the shared
// upstream for as long as the viewer keeps the request open.
func NVRStreamHandler(w http.ResponseWriter, r *http.Request) {
	camera := chi.URLParam(r, "camera")
	recorder, err := nvrCameraDriver(r.Context(), chi.URLParam(r, "id"), camera)
	if err != nil {
		writeAppError(w, err)
		return
	}
	sub, err := recorder.Stream(r.Context(), camera, imageOptions(r))
	if err != nil {
		writeAppError(w, nvrError(err))
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace;boundary="+streamBoundary)
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-sub.Frames:
			if !ok {
				return
			}
			if err := frigate.WriteFrame(w, streamBoundary, frame); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// NVREventMediaHandler relays one event's snapshot, thumbnail, or clip,
// checking the event's camera against the viewer's grant first.
func NVREventMediaHandler(w http.ResponseWriter, r *http.Request) {
	connID, id, kind := chi.URLParam(r, "id"), chi.URLParam(r, "event"), chi.URLParam(r, "media")
	if !frigate.EventIDRe.MatchString(id) {
		writeAppError(w, apperrors.ErrValidation.WithMessage("invalid event id"))
		return
	}
	if kind != nvr.MediaSnapshot && kind != nvr.MediaThumbnail && kind != nvr.MediaClip {
		writeAppError(w, apperrors.ErrNotFound.WithMessage("events have a snapshot, a thumbnail, and a clip"))
		return
	}
	cams, all, err := nvrVisibleCameras(r.Context(), connID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	recorder, err := connops.NVRDriver(connID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !all {
		event, err := recorder.Event(r.Context(), id)
		if err != nil {
			writeAppError(w, nvrError(err))
			return
		}
		if !cams[event.Camera] {
			writeAppError(w, apperrors.ErrForbidden.WithMessage("this dashboard does not show camera "+event.Camera))
			return
		}
	}
	resp, err := recorder.EventMedia(r.Context(), id, kind, r.Header.Get("Range"))
	if err != nil {
		writeAppError(w, nvrError(err))
		return
	}
	fallback := map[string]string{nvr.MediaSnapshot: "image/jpeg", nvr.MediaThumbnail: "image/webp", nvr.MediaClip: "video/mp4"}[kind]
	copyMedia(w, resp, fallback)
}

// NVRCamerasInput names the NVR connection.
type NVRCamerasInput struct {
	ID string `path:"id" description:"NVR connection UUID."`
}

// NVRCamerasOutput is a recorder's cameras.
type NVRCamerasOutput struct {
	Cameras []nvr.Camera `json:"cameras" description:"Cameras with zones, tracked objects, and whether each receives frames."`
}

// NVRCamerasHandler returns the interactor listing a recorder's cameras for
// pickers.
func NVRCamerasHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in NVRCamerasInput, out *apiutil.Success[NVRCamerasOutput]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil || !(auth.Can(ctx, uc.UserID, perm.PermConnectionsRead) || auth.Can(ctx, uc.UserID, perm.PermDashboardsWrite)) {
			return apperrors.ErrForbidden.WithMessage("listing cameras needs connection read or dashboard write")
		}
		recorder, err := connops.NVRDriver(in.ID)
		if err != nil {
			return err
		}
		out.Data = NVRCamerasOutput{Cameras: recorder.Cameras()}
		return nil
	})
	u.SetTitle("List NVR Cameras")
	u.SetDescription("Lists an NVR connection's cameras with their zones, tracked objects, and whether each is receiving frames.")
	u.SetTags("Connections")
	return u
}

// NVREventsInput filters an event listing.
type NVREventsInput struct {
	ID      string `path:"id" description:"NVR connection UUID."`
	Cameras string `query:"cameras" description:"Comma-separated camera names; empty lists every camera the caller may see."`
	Labels  string `query:"labels" description:"Comma-separated labels, such as person,car."`
	Zones   string `query:"zones" description:"Comma-separated zone names."`
	Limit   int    `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Events to return, 1 to 100."`
}

// NVREventsOutput is recent events.
type NVREventsOutput struct {
	Events []nvr.Event `json:"events" description:"Events newest first, with relay paths for their snapshot, thumbnail, and clip."`
}

// splitList reads a comma-separated query value.
func splitList(raw string) []string {
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ListNVREvents lists events limited to the cameras the caller may see.
func ListNVREvents(ctx context.Context, connID string, q nvr.EventQuery) ([]nvr.Event, error) {
	cams, all, err := nvrVisibleCameras(ctx, connID)
	if err != nil {
		return nil, err
	}
	if q.Limit < 1 || q.Limit > nvr.MaxEvents {
		q.Limit = 20
	}
	for _, c := range q.Cameras {
		if !frigate.CameraNameRe.MatchString(c) {
			return nil, apperrors.ErrValidation.WithMessage("invalid camera name " + c)
		}
	}
	if !all {
		// A viewer sees only the cameras the dashboard shows, asked for or
		// not.
		var allowed []string
		if len(q.Cameras) == 0 {
			for c := range cams {
				allowed = append(allowed, c)
			}
		} else {
			for _, c := range q.Cameras {
				if cams[c] {
					allowed = append(allowed, c)
				}
			}
		}
		if len(allowed) == 0 {
			return []nvr.Event{}, nil
		}
		q.Cameras = allowed
	}
	recorder, err := connops.NVRDriver(connID)
	if err != nil {
		return nil, err
	}
	events, err := recorder.Events(ctx, q)
	if err != nil {
		return nil, nvrError(err)
	}
	return events, nil
}

// NVREventsHandler returns the interactor listing recent events.
func NVREventsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in NVREventsInput, out *apiutil.Success[NVREventsOutput]) error {
		events, err := ListNVREvents(ctx, in.ID, nvr.EventQuery{Cameras: splitList(in.Cameras),
			Labels: splitList(in.Labels), Zones: splitList(in.Zones), Limit: in.Limit})
		if err != nil {
			return err
		}
		out.Data = NVREventsOutput{Events: events}
		return nil
	})
	u.SetTitle("List NVR Events")
	u.SetDescription("Lists recent events of an NVR connection, newest first, limited to the cameras the caller may see.")
	u.SetTags("Connections")
	return u
}
