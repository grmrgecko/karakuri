package api

import (
	"context"
	"github.com/grmrgecko/karakuri/pkg/access"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// parseMemoryUUID reads a UUID path parameter for the memory endpoints.
func parseMemoryUUID(raw, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("invalid " + what + " ID")
	}
	return id, nil
}

// MemoryStoreListHandler returns the interactor that lists every memory store
// with what it holds.
func MemoryStoreListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]memories.StoreSnapshot]) error {
		out.Data = memories.S.ListStores()
		return nil
	})
	u.SetTitle("List Memory Stores")
	u.SetDescription("Returns every memory store with its fact, turn, and session counts.")
	u.SetTags("Memories")
	return u
}

// MemoryStoreParamsInput is the shared definition body for creating and
// updating a store.
type MemoryStoreParamsInput struct {
	Name            string  `json:"name" required:"true" minLength:"1" description:"Display name, unique across memory stores."`
	Description     string  `json:"description,omitempty" description:"What the store remembers."`
	EmbedConnection string  `json:"embed_connection,omitempty" description:"AI provider connection UUID for embeddings; empty disables semantic recall."`
	EmbedModel      string  `json:"embed_model,omitempty" description:"Embedding model identifier."`
	HalfLifeDays    float64 `json:"half_life_days,omitempty" description:"Recency decay halflife in days; 0 uses the default."`
	RecencyWeight   float64 `json:"recency_weight,omitempty" minimum:"0" maximum:"1" description:"How much recency counts in recall scoring."`
	MaxSessionTurns int     `json:"max_session_turns,omitempty" description:"Conversation turns kept per session; 0 uses the default."`
	MaxMemories     int     `json:"max_memories,omitempty" description:"Fact cap; 0 is unlimited."`
}

func (in MemoryStoreParamsInput) params() memories.StoreParams {
	return memories.StoreParams{
		Name:            in.Name,
		Description:     in.Description,
		EmbedConnection: in.EmbedConnection,
		EmbedModel:      in.EmbedModel,
		HalfLifeDays:    in.HalfLifeDays,
		RecencyWeight:   in.RecencyWeight,
		MaxSessionTurns: in.MaxSessionTurns,
		MaxMemories:     in.MaxMemories,
	}
}

// MemoryStoreCreateHandler returns the interactor that defines a memory store.
func MemoryStoreCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryStoreParamsInput, out *apiutil.Success[memories.StoreSnapshot]) error {
		if err := access.UseConnections(ctx, callerID(ctx), in.EmbedConnection); err != nil {
			return err
		}
		snap, err := memories.S.CreateStore(in.params())
		if err != nil {
			return memoryWriteError(err)
		}
		out.Data = snap
		out.Message = "Memory store created"
		return nil
	})
	u.SetTitle("Create Memory Store")
	u.SetDescription("Defines a memory store for AI agents to remember into.")
	u.SetTags("Memories")
	return u
}

// MemoryStoreGetInput identifies one memory store.
type MemoryStoreGetInput struct {
	ID string `path:"id" format:"uuid" description:"Memory store UUID."`
}

// MemoryStoreGetHandler returns the interactor that reads one store.
func MemoryStoreGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryStoreGetInput, out *apiutil.Success[memories.StoreSnapshot]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		snap, ok := memories.S.GetStore(id)
		if !ok {
			return apperrors.ErrNotFound.WithMessage("memory store not found")
		}
		out.Data = snap
		return nil
	})
	u.SetTitle("Get Memory Store")
	u.SetDescription("Returns one memory store with its counts.")
	u.SetTags("Memories")
	return u
}

// MemoryStoreUpdateInput rewrites a store's definition.
type MemoryStoreUpdateInput struct {
	ID string `path:"id" format:"uuid" description:"Memory store UUID."`
	MemoryStoreParamsInput
}

// MemoryStoreUpdateHandler returns the interactor that rewrites a store.
func MemoryStoreUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryStoreUpdateInput, out *apiutil.Success[memories.StoreSnapshot]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		if err := access.UseConnections(ctx, callerID(ctx), in.EmbedConnection); err != nil {
			return err
		}
		snap, err := memories.S.UpdateStore(id, in.params())
		if err != nil {
			return memoryWriteError(err)
		}
		out.Data = snap
		out.Message = "Memory store updated"
		return nil
	})
	u.SetTitle("Update Memory Store")
	u.SetDescription("Rewrites a memory store's definition. Changing the embedding settings re-embeds everything it holds.")
	u.SetTags("Memories")
	return u
}

// MemoryStoreDeleteHandler returns the interactor that removes a store and
// everything it remembers.
func MemoryStoreDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryStoreGetInput, out *apiutil.Success[struct{}]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		if _, ok := memories.S.GetStore(id); !ok {
			return apperrors.ErrNotFound.WithMessage("memory store not found")
		}
		if err := memories.S.DeleteStore(id); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Message = "Memory store deleted"
		return nil
	})
	u.SetTitle("Delete Memory Store")
	u.SetDescription("Removes a memory store and every memory it holds.")
	u.SetTags("Memories")
	return u
}

// MemoryBrowseInput shapes a browse or search of one store.
type MemoryBrowseInput struct {
	ID      string `path:"id" format:"uuid" description:"Memory store UUID."`
	Query   string `query:"query" description:"Recall query; empty lists newest first."`
	Session string `query:"session" description:"Filter to one session."`
	Kind    string `query:"kind" enum:"fact,turn" description:"Filter to one kind."`
	Limit   int    `query:"limit" description:"Result cap; 0 uses the default."`
}

// MemoryBrowseHandler returns the interactor behind the settings page's
// memory browser: recency-ordered without a query, recall-scored with one.
func MemoryBrowseHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryBrowseInput, out *apiutil.Success[[]memories.Result]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		if _, ok := memories.S.GetStore(id); !ok {
			return apperrors.ErrNotFound.WithMessage("memory store not found")
		}
		out.Data = memories.S.Browse(ctx, id, in.Query, in.Session, in.Kind, in.Limit)
		return nil
	})
	u.SetTitle("Browse Memories")
	u.SetDescription("Lists or searches a store's memories. A query scores them the way agent recall does.")
	u.SetTags("Memories")
	return u
}

// MemoryParamsInput is the shared body for creating and editing a memory.
type MemoryParamsInput struct {
	Kind       string   `json:"kind,omitempty" enum:"fact,turn" description:"Memory kind; defaults to fact."`
	Session    string   `json:"session,omitempty" description:"Session key; empty is global to the store."`
	Role       string   `json:"role,omitempty" description:"Turn role: user or assistant."`
	Text       string   `json:"text" required:"true" minLength:"1" description:"The remembered text."`
	Tags       []string `json:"tags,omitempty" description:"Tags matched alongside the text in recall."`
	Importance float64  `json:"importance,omitempty" minimum:"0" maximum:"1" description:"Recall weight; 0 uses the default."`
	ExpiresAt  *string  `json:"expires_at,omitempty" description:"Optional RFC 3339 expiry."`
}

func (in MemoryParamsInput) params() (memories.MemoryParams, error) {
	p := memories.MemoryParams{
		Kind:       in.Kind,
		Session:    in.Session,
		Role:       in.Role,
		Text:       in.Text,
		Tags:       in.Tags,
		Importance: in.Importance,
	}
	if in.ExpiresAt != nil && strings.TrimSpace(*in.ExpiresAt) != "" {
		at, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			return p, apperrors.ErrValidation.WithMessage("expires_at is not an RFC 3339 timestamp")
		}
		p.ExpiresAt = &at
	}
	return p, nil
}

// MemoryCreateInput adds one memory to a store.
type MemoryCreateInput struct {
	ID string `path:"id" format:"uuid" description:"Memory store UUID."`
	MemoryParamsInput
}

// MemoryCreateHandler returns the interactor that stores a memory by hand.
func MemoryCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryCreateInput, out *apiutil.Success[memories.Item]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		p, err := in.params()
		if err != nil {
			return err
		}
		item, err := memories.S.Remember(id, p)
		if err != nil {
			return memoryWriteError(err)
		}
		out.Data = item
		out.Message = "Memory stored"
		return nil
	})
	u.SetTitle("Create Memory")
	u.SetDescription("Stores one memory in a store.")
	u.SetTags("Memories")
	return u
}

// MemoryUpdateInput edits one memory.
type MemoryUpdateInput struct {
	ID       string `path:"id" format:"uuid" description:"Memory store UUID."`
	MemoryID string `path:"memoryId" format:"uuid" description:"Memory UUID."`
	MemoryParamsInput
}

// MemoryUpdateHandler returns the interactor that edits a memory.
func MemoryUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryUpdateInput, out *apiutil.Success[memories.Item]) error {
		storeID, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		memID, err := parseMemoryUUID(in.MemoryID, "memory")
		if err != nil {
			return err
		}
		p, err := in.params()
		if err != nil {
			return err
		}
		item, err := memories.S.UpdateMemory(storeID, memID, p)
		if err != nil {
			return memoryWriteError(err)
		}
		out.Data = item
		out.Message = "Memory updated"
		return nil
	})
	u.SetTitle("Update Memory")
	u.SetDescription("Edits a memory. Changing its text re-embeds it.")
	u.SetTags("Memories")
	return u
}

// MemoryDeleteInput identifies one memory.
type MemoryDeleteInput struct {
	ID       string `path:"id" format:"uuid" description:"Memory store UUID."`
	MemoryID string `path:"memoryId" format:"uuid" description:"Memory UUID."`
}

// MemoryDeleteHandler returns the interactor that forgets one memory.
func MemoryDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryDeleteInput, out *apiutil.Success[struct{}]) error {
		storeID, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		memID, err := parseMemoryUUID(in.MemoryID, "memory")
		if err != nil {
			return err
		}
		if _, ok := memories.S.GetMemory(storeID, memID); !ok {
			return apperrors.ErrNotFound.WithMessage("memory not found")
		}
		if err := memories.S.DeleteMemory(storeID, memID); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Message = "Memory deleted"
		return nil
	})
	u.SetTitle("Delete Memory")
	u.SetDescription("Removes one memory.")
	u.SetTags("Memories")
	return u
}

// MemorySessionListHandler returns the interactor that lists a store's
// sessions.
func MemorySessionListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemoryStoreGetInput, out *apiutil.Success[[]memories.SessionInfo]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		if _, ok := memories.S.GetStore(id); !ok {
			return apperrors.ErrNotFound.WithMessage("memory store not found")
		}
		out.Data = memories.S.Sessions(id)
		return nil
	})
	u.SetTitle("List Memory Sessions")
	u.SetDescription("Lists a store's conversation sessions, newest activity first.")
	u.SetTags("Memories")
	return u
}

// MemorySessionClearInput identifies one session in a store.
type MemorySessionClearInput struct {
	ID      string `path:"id" format:"uuid" description:"Memory store UUID."`
	Session string `query:"session" required:"true" description:"Session key to forget."`
}

// MemorySessionClearHandler returns the interactor that forgets a session's
// transcript and its scoped facts.
func MemorySessionClearHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MemorySessionClearInput, out *apiutil.Success[struct{}]) error {
		id, err := parseMemoryUUID(in.ID, "memory store")
		if err != nil {
			return err
		}
		if _, ok := memories.S.GetStore(id); !ok {
			return apperrors.ErrNotFound.WithMessage("memory store not found")
		}
		if err := memories.S.ClearSession(id, in.Session); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Message = "Session cleared"
		return nil
	})
	u.SetTitle("Clear Memory Session")
	u.SetDescription("Forgets one session's transcript and session-scoped facts.")
	u.SetTags("Memories")
	return u
}

// memoryWriteError maps a store failure onto the right status.
func memoryWriteError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "unique"):
		return apperrors.ErrConflict.WithMessage("a memory store with this name already exists")
	case strings.Contains(msg, "not found"):
		return apperrors.ErrNotFound.WithMessage(msg)
	case strings.Contains(msg, "required"), strings.Contains(msg, "must be"),
		strings.Contains(msg, "cannot be"), strings.Contains(msg, "needs an"):
		return apperrors.ErrValidation.WithMessage(msg)
	}
	return apperrors.ErrInternal.WithError(err)
}

// RegisterMemoryRoutes registers the memory store endpoints. Everything is a
// settings-page or agent concern, so reads gate on api:memories:read and
// writes on api:memories:write with no public path.
func RegisterMemoryRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	read := []func(http.Handler) http.Handler{middleware.Auth(), middleware.RequirePermission(perm.PermMemoriesRead), bearerDoc}
	write := []func(http.Handler) http.Handler{middleware.Auth(), middleware.RequirePermission(perm.PermMemoriesWrite), bearerDoc}

	s.With(read...).Method(http.MethodGet, "/memory_stores",
		nethttp.NewHandler(MemoryStoreListHandler()))
	s.With(write...).Method(http.MethodPost, "/memory_stores",
		nethttp.NewHandler(MemoryStoreCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(read...).Method(http.MethodGet, "/memory_stores/{id}",
		nethttp.NewHandler(MemoryStoreGetHandler()))
	s.With(write...).Method(http.MethodPut, "/memory_stores/{id}",
		nethttp.NewHandler(MemoryStoreUpdateHandler()))
	s.With(write...).Method(http.MethodDelete, "/memory_stores/{id}",
		nethttp.NewHandler(MemoryStoreDeleteHandler()))
	s.With(read...).Method(http.MethodGet, "/memory_stores/{id}/memories",
		nethttp.NewHandler(MemoryBrowseHandler()))
	s.With(write...).Method(http.MethodPost, "/memory_stores/{id}/memories",
		nethttp.NewHandler(MemoryCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(write...).Method(http.MethodPut, "/memory_stores/{id}/memories/{memoryId}",
		nethttp.NewHandler(MemoryUpdateHandler()))
	s.With(write...).Method(http.MethodDelete, "/memory_stores/{id}/memories/{memoryId}",
		nethttp.NewHandler(MemoryDeleteHandler()))
	s.With(read...).Method(http.MethodGet, "/memory_stores/{id}/sessions",
		nethttp.NewHandler(MemorySessionListHandler()))
	s.With(write...).Method(http.MethodDelete, "/memory_stores/{id}/sessions",
		nethttp.NewHandler(MemorySessionClearHandler()))
}
