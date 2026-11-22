package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"net/http"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/agentops"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/ca"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"gorm.io/gorm"
)

// agentHub is wired by the server at startup. It is nil when the agent listener
// is disabled, and handlers then report the subsystem as unavailable rather
// than panicking.
var agentHub *agenthub.Hub

// SetAgentHub installs the agent hub used by handlers.
func SetAgentHub(h *agenthub.Hub) {
	agentHub = h
	agentops.SetHub(h)
}

// AgentItem is the host status shared with MCP.
type AgentItem = agentops.Item

// AgentListHandler lists every enrolled agent.
func AgentListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]AgentItem]) error {
		items, err := agentops.ListFor(ctx, callerID(ctx))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = items
		return nil
	})
	u.SetTitle("List Agents")
	u.SetTags("Agents")
	return u
}

// AgentUpdateInput carries the editable fields of an agent.
type AgentUpdateInput struct {
	ID          string `path:"id" format:"uuid" description:"Agent UUID."`
	Name        string `json:"name" description:"Display name and palette folder."`
	Description string `json:"description" description:"Operator notes."`
	Enabled     bool   `json:"enabled" description:"Whether the agent may connect."`
}

// AgentUpdateHandler renames, describes, or enables an agent. A rename or a
// disable drops the live session: the folder label rides the connection, and a
// disabled agent must be cut off, so both take effect on the next connect.
func AgentUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AgentUpdateInput, out *apiutil.Success[AgentItem]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid agent id")
		}
		agent, err := access.Agent(ctx, callerID(ctx), in.ID, access.Edit)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return apperrors.ErrValidation.WithMessage("name is required")
		}
		// A name collision with another agent is rejected rather than silently
		// suffixed: an operator typing a name meant that name.
		if existing, err := models.FindAgentByName(name); err == nil && existing.ID != id {
			return apperrors.ErrValidation.WithMessage("another agent already uses that name")
		}

		dropSession := agent.Name != name || (agent.Enabled && !in.Enabled)
		if err := models.UpdateAgentDetails(id, name, in.Description, in.Enabled); err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		agent.Name = name
		agent.Description = in.Description
		agent.Enabled = in.Enabled
		if dropSession && agentHub != nil {
			agentHub.DropAgent(id)
		}
		out.Data = agentops.DescribeFor(ctx, callerID(ctx), agent)
		return nil
	})
	u.SetTitle("Update Agent")
	u.SetTags("Agents")
	return u
}

// AgentIDInput names an agent by path.
type AgentIDInput struct {
	ID string `path:"id" format:"uuid" description:"Agent UUID."`
}

// AgentDeleteHandler drops the session and removes the agent record. Its
// certificate is revoked so a held key cannot reconnect against a re-created
// record with the same id.
func AgentDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AgentIDInput, out *apiutil.Success[struct{}]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid agent id")
		}
		agent, err := access.Agent(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		if agent.CertSerial != "" {
			_ = models.RevokeAgentSerial(agent.CertSerial, id, "agent deleted")
		}
		_ = models.DeleteAgentAccess(nil, id)
		if agentHub != nil {
			agentHub.DropAgent(id)
		}
		if err := models.DeleteAgent(id); err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Message = "agent deleted"
		return nil
	})
	u.SetTitle("Delete Agent")
	u.SetTags("Agents")
	return u
}

// AgentRevokeHandler revokes an agent's certificate and disables it, cutting off
// a host whose key may be compromised without deleting its record.
func AgentRevokeHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AgentIDInput, out *apiutil.Success[AgentItem]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid agent id")
		}
		agent, err := access.Agent(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		if agent.CertSerial != "" {
			if err := models.RevokeAgentSerial(agent.CertSerial, id, "revoked by operator"); err != nil {
				return apperrors.ErrInternal.WithMessage(err.Error())
			}
		}
		agent.Enabled = false
		if err := models.SaveAgent(agent); err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		if agentHub != nil {
			agentHub.DropAgent(id)
		}
		out.Data = agentops.DescribeFor(ctx, callerID(ctx), agent)
		return nil
	})
	u.SetTitle("Revoke Agent")
	u.SetTags("Agents")
	return u
}

// AgentTokenInput is what an operator sets when minting an enrollment token.
type AgentTokenInput struct {
	Name        string `json:"name" description:"Name the enrolled agent inherits; falls back to its hostname."`
	Description string `json:"description" description:"Description the enrolled agent inherits."`
}

// AgentTokenResult is the minted join token, shown once, plus a ready-to-run
// enroll command so an operator can copy one line to the host.
type AgentTokenResult struct {
	Token     string    `json:"token" description:"The join token — the server's CA and a one-time secret — shown once."`
	Server    string    `json:"server,omitempty" description:"Suggested server address to dial."`
	Command   string    `json:"command,omitempty" description:"Ready-to-run enroll command."`
	ExpiresAt time.Time `json:"expires_at" description:"When the token stops being accepted."`
}

// AgentTokenCreateHandler mints an enrollment token.
func AgentTokenCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AgentTokenInput, out *apiutil.Success[AgentTokenResult]) error {
		if agentHub == nil {
			return apperrors.ErrValidation.WithMessage("the agent listener is disabled; set agents.listen in the config")
		}
		uc := middleware.GetUser(ctx)
		var createdBy *uuid.UUID
		if uc != nil {
			createdBy = &uc.UserID
		}
		ttl := time.Duration(config.C.Agents.EnrollTTL) * time.Second
		raw, tok, err := models.NewEnrollmentToken(strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), createdBy, ttl)
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		// The token carries the CA, so the agent verifies the server on first
		// contact with nothing pre-installed and never has to take the reply's
		// word for what to trust.
		join, err := ca.EncodeJoin(agentHub.Bundle().PEM(), raw)
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		server := suggestedServer()
		out.Data = AgentTokenResult{
			Token:     join,
			Server:    server,
			Command:   enrollCommand(server, join),
			ExpiresAt: tok.ExpiresAt,
		}
		return nil
	})
	u.SetTitle("Mint Enrollment Token")
	u.SetTags("Agents")
	return u
}

// AgentTokenItem is a pending token as the panel lists it. The raw token is
// never returned again.
type AgentTokenItem struct {
	ID          string     `json:"id" description:"Token identifier."`
	Name        string     `json:"name,omitempty" description:"Name the enrolled agent inherits."`
	Description string     `json:"description,omitempty" description:"Description the enrolled agent inherits."`
	ExpiresAt   time.Time  `json:"expires_at" description:"When the token expires."`
	UsedAt      *time.Time `json:"used_at,omitempty" description:"When the token was used; null while pending."`
	CreatedAt   time.Time  `json:"created_at" description:"When the token was minted."`
}

// AgentTokenListHandler lists enrollment tokens, so the panel can show pending
// ones and let an operator revoke an unused token.
func AgentTokenListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]AgentTokenItem]) error {
		toks, err := models.ListEnrollmentTokens()
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		// Tokens are the minter's own; a manager sees everyone's.
		uid := callerID(ctx)
		manager := auth.Can(ctx, uid, perm.PermAgentsManage)
		items := make([]AgentTokenItem, 0, len(toks))
		for _, t := range toks {
			if !manager && (t.CreatedBy == nil || *t.CreatedBy != uid) {
				continue
			}
			items = append(items, AgentTokenItem{
				ID:          t.ID.String(),
				Name:        t.Name,
				Description: t.Description,
				ExpiresAt:   t.ExpiresAt,
				UsedAt:      t.UsedAt,
				CreatedAt:   t.CreatedAt,
			})
		}
		out.Data = items
		return nil
	})
	u.SetTitle("List Enrollment Tokens")
	u.SetTags("Agents")
	return u
}

// AgentTokenDeleteHandler removes a pending enrollment token.
func AgentTokenDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AgentIDInput, out *apiutil.Success[struct{}]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid token id")
		}
		uid := callerID(ctx)
		if !auth.Can(ctx, uid, perm.PermAgentsManage) {
			var tok models.AgentEnrollmentToken
			if err := db.DB.First(&tok, "id = ?", id).Error; err != nil || tok.CreatedBy == nil || *tok.CreatedBy != uid {
				return apperrors.ErrNotFound.WithMessage("token not found")
			}
		}
		if err := models.DeleteEnrollmentToken(id); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperrors.ErrNotFound.WithMessage("token not found")
			}
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Message = "token deleted"
		return nil
	})
	u.SetTitle("Delete Enrollment Token")
	u.SetTags("Agents")
	return u
}

// suggestedServer guesses the address an agent should dial: the listener's port
// on the host the listener names, or, since it usually names every interface,
// on whatever the browser was told this server is. It is a suggestion for the
// copy-paste command and nothing verifies against it, so a wrong guess costs an
// edit rather than a failed handshake.
func suggestedServer() string {
	host, port, err := net.SplitHostPort(config.C.Agents.Listen)
	if err != nil {
		return ""
	}
	if isWildcardHost(host) {
		host = baseURLHost()
	}
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

// isWildcardHost reports whether a listen host names every interface rather
// than a reachable address.
func isWildcardHost(host string) bool {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}

// baseURLHost pulls the hostname out of the configured base URL, falling back
// to the machine's own hostname.
func baseURLHost() string {
	if u, err := url.Parse(config.C.Server.BaseURL); err == nil && u.Hostname() != "" {
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
			return h
		}
	}
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// enrollCommand renders the copy-paste enrollment command, leaving a
// placeholder when no address could be suggested.
func enrollCommand(server, token string) string {
	if server == "" {
		server = "<host:port>"
	}
	return fmt.Sprintf("karakuri-agent enroll --server %s --token %s --service", server, token)
}

// AgentCAHandler reports the state of the agent CA: when the signing root
// expires, whether a rollover is under way, and when the listener's own
// certificate runs out. A ten-year timer nobody can see is a ten-year timer
// nobody renews.
func AgentCAHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[agenthub.CAStatus]) error {
		if agentHub == nil {
			return apperrors.ErrValidation.WithMessage("the agent listener is disabled; set agents.listen in the config")
		}
		out.Data = agentHub.CAStatus()
		return nil
	})
	u.SetTitle("Agent CA Status")
	u.SetTags("Agents")
	return u
}

// AgentCARotateHandler starts a CA rollover by hand. The successor root is
// generated immediately, but it does not take over until every agent has
// renewed onto it, so pressing this does not cut anything off.
func AgentCARotateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[agenthub.CAStatus]) error {
		if agentHub == nil {
			return apperrors.ErrValidation.WithMessage("the agent listener is disabled; set agents.listen in the config")
		}
		if err := agentHub.RotateCA(); err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Data = agentHub.CAStatus()
		out.Message = "CA rollover started; agents move to the new root as they renew"
		return nil
	})
	u.SetTitle("Rotate Agent CA")
	u.SetTags("Agents")
	return u
}

// RegisterAgentRoutes wires the agent management API.
func RegisterAgentRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	read := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAgentsRead), bearerDoc)
	read.Method(http.MethodGet, "/agents", nethttp.NewHandler(AgentListHandler()))
	read.Method(http.MethodGet, "/agents/tokens", nethttp.NewHandler(AgentTokenListHandler()))
	read.Method(http.MethodGet, "/agents/ca", nethttp.NewHandler(AgentCAHandler()))

	write := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAgentsWrite), bearerDoc)
	write.Method(http.MethodPut, "/agents/{id}", nethttp.NewHandler(AgentUpdateHandler()))
	write.Method(http.MethodDelete, "/agents/{id}", nethttp.NewHandler(AgentDeleteHandler()))

	enroll := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAgentsEnroll), bearerDoc)
	enroll.Method(http.MethodPost, "/agents/tokens", nethttp.NewHandler(AgentTokenCreateHandler()))
	enroll.Method(http.MethodDelete, "/agents/tokens/{id}", nethttp.NewHandler(AgentTokenDeleteHandler()))

	revoke := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAgentsRevoke), bearerDoc)
	revoke.Method(http.MethodPost, "/agents/{id}/revoke", nethttp.NewHandler(AgentRevokeHandler()))
	// Rolling the CA is revocation-shaped work: it decides what the whole fleet
	// trusts, so it sits behind the same gate rather than plain write access.
	revoke.Method(http.MethodPost, "/agents/ca/rotate", nethttp.NewHandler(AgentCARotateHandler()))
}
