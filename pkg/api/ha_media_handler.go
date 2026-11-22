package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/swaggest/usecase"
)

// entityIDRe matches a Home Assistant entity ID. It is checked before an ID
// is placed in a Home Assistant URL path.
var entityIDRe = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

// Limits on what the media routes relay.
const (
	maxHistoryHours = 168
	maxImageBytes   = 10 << 20
	imageTimeout    = 15 * time.Second
)

// haEntityDriver authorizes a read of one entity and returns the connection's
// driver. The rule is the one entity states follow: connection read sees
// everything, and a dashboard viewer sees the entities its dashboard binds.
// domain, when set, is the only entity domain the route serves.
func haEntityDriver(ctx context.Context, connID, entityID, domain string) (*homeassistant.Driver, error) {
	if !entityIDRe.MatchString(entityID) {
		return nil, apperrors.ErrValidation.WithMessage("invalid entity ID")
	}
	if domain != "" && !strings.HasPrefix(entityID, domain+".") {
		return nil, apperrors.ErrValidation.WithMessage("this route serves " + domain + " entities")
	}
	allowed, err := haVisibleEntities(ctx, connID)
	if err != nil {
		return nil, err
	}
	if allowed != nil && !allowed[entityID] {
		return nil, apperrors.ErrForbidden.WithMessage("this dashboard does not show " + entityID)
	}
	return connops.HADriver(connID)
}

// HAHistoryInput names an entity and how far back to read.
type HAHistoryInput struct {
	ID     string `path:"id" description:"Connection UUID."`
	Entity string `path:"entity" description:"Entity ID, e.g. sensor.outside_temperature."`
	Hours  int    `query:"hours" default:"24" minimum:"1" maximum:"168" description:"Hours of history to read, 1 to 168."`
}

// HAHistory is one entity's recorded states.
type HAHistory struct {
	EntityID string                       `json:"entity_id" description:"Entity identifier."`
	Unit     string                       `json:"unit,omitempty" description:"Unit of measurement from the entity's current attributes."`
	Points   []homeassistant.HistoryPoint `json:"points" description:"States oldest first; t is Unix milliseconds."`
}

// HAHistoryHandler returns the interactor that reads an entity's recorded
// history for a history graph.
func HAHistoryHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HAHistoryInput, out *apiutil.Success[HAHistory]) error {
		driver, err := haEntityDriver(ctx, in.ID, in.Entity, "")
		if err != nil {
			return err
		}
		hours := in.Hours
		if hours < 1 || hours > maxHistoryHours {
			return apperrors.ErrValidation.WithMessage("hours must be 1 to 168")
		}
		end := time.Now()
		points, err := driver.History(ctx, in.Entity, end.Add(-time.Duration(hours)*time.Hour), end)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("reading history: " + err.Error())
		}
		result := HAHistory{EntityID: in.Entity, Points: points}
		if states, err := driver.StatesCached(ctx); err == nil {
			for _, s := range states {
				if s.EntityID == in.Entity {
					result.Unit, _ = s.Attributes["unit_of_measurement"].(string)
				}
			}
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Get Home Assistant History")
	u.SetDescription("Returns an entity's recorded states for 1 to 168 hours, for history graphs.")
	u.SetTags("Connections")
	return u
}

// HATodoInput names a todo entity.
type HATodoInput struct {
	ID     string `path:"id" description:"Connection UUID."`
	Entity string `path:"entity" description:"Todo entity ID, e.g. todo.shopping_list."`
}

// HATodo is a to-do list's items.
type HATodo struct {
	Items []homeassistant.TodoItem `json:"items" description:"List items."`
}

// HATodoHandler returns the interactor that reads a to-do list's items.
// Changing them goes through the service call route and its control policy.
func HATodoHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HATodoInput, out *apiutil.Success[HATodo]) error {
		driver, err := haEntityDriver(ctx, in.ID, in.Entity, "todo")
		if err != nil {
			return err
		}
		items, err := driver.TodoItems(ctx, in.Entity)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("reading the list: " + err.Error())
		}
		if items == nil {
			items = []homeassistant.TodoItem{}
		}
		out.Data = HATodo{Items: items}
		return nil
	})
	u.SetTitle("Get Home Assistant To-do Items")
	u.SetDescription("Returns the items of a todo entity.")
	u.SetTags("Connections")
	return u
}

// writeAppError answers a raw handler's failure in the API's JSON error shape.
func writeAppError(w http.ResponseWriter, err error) {
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		appErr = apperrors.ErrInternal.WithError(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.HTTPStatus())
	_ = json.NewEncoder(w).Encode(appErr)
}

// relayImage copies one image response to the client with a size cap.
func relayImage(w http.ResponseWriter, r *http.Request, driver *homeassistant.Driver, path string) {
	ctx, cancel := context.WithTimeout(r.Context(), imageTimeout)
	defer cancel()
	resp, err := driver.Fetch(ctx, path)
	if err != nil {
		writeAppError(w, apperrors.ErrBadGateway.WithMessage(err.Error()))
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "image/") {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxImageBytes))
}

// HACameraSnapshotHandler relays a bound camera's current image.
func HACameraSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	driver, err := haEntityDriver(r.Context(), chi.URLParam(r, "id"), entity, "camera")
	if err != nil {
		writeAppError(w, err)
		return
	}
	relayImage(w, r, driver, "/api/camera_proxy/"+entity)
}

// HACameraStreamHandler relays a bound camera's MJPEG stream for as long as
// the viewer keeps the request open. Home Assistant serves it for every
// camera it supports, at a frame rate it chooses.
func HACameraStreamHandler(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	driver, err := haEntityDriver(r.Context(), chi.URLParam(r, "id"), entity, "camera")
	if err != nil {
		writeAppError(w, err)
		return
	}
	resp, err := driver.Fetch(r.Context(), "/api/camera_proxy_stream/"+entity)
	if err != nil {
		writeAppError(w, apperrors.ErrBadGateway.WithMessage(err.Error()))
		return
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/") {
		writeAppError(w, apperrors.ErrBadGateway.WithMessage("home assistant did not answer with a stream"))
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// HAMediaArtHandler relays a bound media player's cover art when Home
// Assistant hosts it. Art hosted elsewhere is an absolute URL the browser
// loads itself, so it is answered with not found. The picture's query carries
// Home Assistant's per-entity access token, which viewers already read in the
// entity's attributes; the connection's own token is added here and never
// returned.
func HAMediaArtHandler(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	driver, err := haEntityDriver(r.Context(), chi.URLParam(r, "id"), entity, "media_player")
	if err != nil {
		writeAppError(w, err)
		return
	}
	// The widget passes the entity_picture it holds, which changes with
	// every track; it is accepted only as this entity's own proxy path.
	// Without one, the cached state supplies it.
	picture := r.URL.Query().Get("picture")
	if picture != "" && !strings.HasPrefix(picture, "/api/media_player_proxy/"+entity+"?") {
		writeAppError(w, apperrors.ErrValidation.WithMessage("picture must be this player's Home Assistant proxy path"))
		return
	}
	if picture == "" {
		states, err := driver.StatesCached(r.Context())
		if err != nil {
			writeAppError(w, apperrors.ErrBadGateway.WithMessage("home assistant is not connected: "+err.Error()))
			return
		}
		for _, s := range states {
			if s.EntityID == entity {
				picture, _ = s.Attributes["entity_picture"].(string)
			}
		}
	}
	if !strings.HasPrefix(picture, "/api/") {
		writeAppError(w, apperrors.ErrNotFound.WithMessage("no Home Assistant hosted art for "+entity))
		return
	}
	relayImage(w, r, driver, picture)
}
