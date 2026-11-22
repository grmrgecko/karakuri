package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// auditRoute describes one administrative change the security log records.
type auditRoute struct {
	method  string
	pattern *regexp.Regexp
	// describe builds the entry from the path parameters, the request body,
	// the response body, and what before looked up ahead of the change.
	describe func(a auditCall) (events.Subject, string)
	// before looks up names a delete removes, ahead of the handler.
	before func(params []string) map[string]string
}

// auditCall is one finished administrative request.
type auditCall struct {
	params []string
	body   map[string]any
	resp   map[string]any
	before map[string]string
	actor  events.Actor
}

// secretFields never reach the log, not even by name in a changed-field list.
var secretFields = map[string]bool{"password": true, "new_password": true, "current_password": true, "old_password": true, "token": true, "key": true}

func (a auditCall) str(key string) string {
	if v, ok := a.body[key].(string); ok {
		return v
	}
	return ""
}

func (a auditCall) num(key string) string {
	switch v := a.body[key].(type) {
	case float64:
		return fmt.Sprint(uint(v))
	case string:
		return v
	}
	return ""
}

// respID reads data.id from the response.
func (a auditCall) respID() string {
	if data, ok := a.resp["data"].(map[string]any); ok {
		switch v := data["id"].(type) {
		case string:
			return v
		case float64:
			return fmt.Sprint(uint(v))
		}
	}
	return ""
}

// fields lists the request's field names, secrets left out.
func (a auditCall) fields() string {
	var keys []string
	for k := range a.body {
		if !secretFields[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// name looks up a display name by table and ID.
func lookupName(table, column, id string) string {
	if id == "" {
		return ""
	}
	var name string
	db.DB.Table(table).Select(column).Where("id = ?", id).Limit(1).Scan(&name)
	if name == "" {
		return id
	}
	return name
}

func userName(id string) string  { return lookupName("users", "username", id) }
func groupName(id string) string { return lookupName("user_groups", "name", id) }
func roleName(id string) string  { return lookupName("roles", "name", id) }

func subject(kind, id, name string) events.Subject {
	return events.Subject{Kind: kind, ID: id, Name: name}
}

var auditRoutes = []auditRoute{
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/users$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("user", a.respID(), a.str("username")), "Created user " + a.str("username")
	}},
	{method: http.MethodPut, pattern: regexp.MustCompile(`^/users/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		n := userName(a.params[0])
		return subject("user", a.params[0], n), fmt.Sprintf("Updated user %s (%s)", n, a.fields())
	}},
	{method: http.MethodPut, pattern: regexp.MustCompile(`^/users/([^/]+)/password$`), describe: func(a auditCall) (events.Subject, string) {
		n := userName(a.params[0])
		return subject("user", a.params[0], n), "Changed the password of " + n
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/users/([^/]+)$`),
		before: func(p []string) map[string]string { return map[string]string{"name": userName(p[0])} },
		describe: func(a auditCall) (events.Subject, string) {
			return subject("user", a.params[0], a.before["name"]), "Deleted user " + a.before["name"]
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/groups$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("group", a.respID(), a.str("name")), "Created group " + a.str("name")
	}},
	{method: http.MethodPut, pattern: regexp.MustCompile(`^/groups/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		n := groupName(a.params[0])
		return subject("group", a.params[0], n), fmt.Sprintf("Updated group %s (%s)", n, a.fields())
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/groups/([^/]+)$`),
		before: func(p []string) map[string]string { return map[string]string{"name": groupName(p[0])} },
		describe: func(a auditCall) (events.Subject, string) {
			return subject("group", a.params[0], a.before["name"]), "Deleted group " + a.before["name"]
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/groups/([^/]+)/members$`), describe: func(a auditCall) (events.Subject, string) {
		g := groupName(a.params[0])
		return subject("group", a.params[0], g), fmt.Sprintf("Added %s to group %s", userName(a.str("user_id")), g)
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/groups/([^/]+)/members/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		g := groupName(a.params[0])
		return subject("group", a.params[0], g), fmt.Sprintf("Removed %s from group %s", userName(a.params[1]), g)
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/groups/([^/]+)/owners$`), describe: func(a auditCall) (events.Subject, string) {
		g := groupName(a.params[0])
		owner := userName(a.str("user_id"))
		if a.str("user_id") == "" {
			owner = "group " + groupName(a.num("owner_group_id"))
		}
		return subject("group", a.params[0], g), fmt.Sprintf("Made %s an owner of group %s", owner, g)
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/groups/([^/]+)/owners/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		g := groupName(a.params[0])
		return subject("group", a.params[0], g), "Removed an owner of group " + g
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/roles$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("role", a.respID(), a.str("name")), "Created role " + a.str("name")
	}},
	{method: http.MethodPut, pattern: regexp.MustCompile(`^/roles/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		n := roleName(a.params[0])
		return subject("role", a.params[0], n), fmt.Sprintf("Updated role %s (%s)", n, a.fields())
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/roles/([^/]+)$`),
		before: func(p []string) map[string]string { return map[string]string{"name": roleName(p[0])} },
		describe: func(a auditCall) (events.Subject, string) {
			return subject("role", a.params[0], a.before["name"]), "Deleted role " + a.before["name"]
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/roles/([^/]+)/permissions$`), describe: func(a auditCall) (events.Subject, string) {
		n := roleName(a.params[0])
		return subject("role", a.params[0], n), fmt.Sprintf("Granted %s to role %s", a.str("permission"), n)
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/roles/([^/]+)/permissions/([^/]+)$`),
		before: func(p []string) map[string]string {
			return map[string]string{"permission": lookupName("role_permissions", "permission", p[1])}
		},
		describe: func(a auditCall) (events.Subject, string) {
			n := roleName(a.params[0])
			return subject("role", a.params[0], n), fmt.Sprintf("Revoked %s from role %s", a.before["permission"], n)
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/role_assignments$`), describe: func(a auditCall) (events.Subject, string) {
		role := a.num("role_id")
		target := userName(a.str("user_id"))
		if a.str("user_id") == "" {
			target = "group " + groupName(a.num("group_id"))
		}
		return subject("role", role, roleName(role)), fmt.Sprintf("Assigned role %s to %s", roleName(role), target)
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/role_assignments/([^/]+)$`),
		before: func(p []string) map[string]string {
			var ra models.RoleAssignment
			out := map[string]string{}
			if db.DB.Where("id = ?", p[0]).First(&ra).Error == nil {
				out["role_id"] = fmt.Sprint(ra.RoleID)
				out["role"] = roleName(fmt.Sprint(ra.RoleID))
				if ra.UserID != nil {
					out["target"] = userName(ra.UserID.String())
				} else if ra.GroupID != nil {
					out["target"] = "group " + groupName(fmt.Sprint(*ra.GroupID))
				}
			}
			return out
		},
		describe: func(a auditCall) (events.Subject, string) {
			return subject("role", a.before["role_id"], a.before["role"]), fmt.Sprintf("Removed role %s from %s", a.before["role"], a.before["target"])
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/api_keys$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("apikey", a.respID(), a.str("name")), "Created API key " + a.str("name")
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/api_keys/([^/]+)$`),
		before: func(p []string) map[string]string {
			return map[string]string{"name": lookupName("api_keys", "name", p[0])}
		},
		describe: func(a auditCall) (events.Subject, string) {
			return subject("apikey", a.params[0], a.before["name"]), "Deleted API key " + a.before["name"]
		}},
	{method: http.MethodPut, pattern: regexp.MustCompile(`^/agents/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		n := lookupName("agents", "name", a.params[0])
		return subject("agent", a.params[0], n), fmt.Sprintf("Updated agent %s (%s)", n, a.fields())
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/agents/tokens/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("agent", "", ""), "Deleted an agent enrollment token"
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/agents/([^/]+)$`),
		before: func(p []string) map[string]string {
			return map[string]string{"name": lookupName("agents", "name", p[0])}
		},
		describe: func(a auditCall) (events.Subject, string) {
			return subject("agent", a.params[0], a.before["name"]), "Deleted agent " + a.before["name"]
		}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/agents/tokens$`), describe: func(a auditCall) (events.Subject, string) {
		name := a.str("name")
		if name == "" {
			name = "a new agent"
		}
		return subject("agent", "", name), "Created an enrollment token for " + name
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/agents/([^/]+)/revoke$`), describe: func(a auditCall) (events.Subject, string) {
		n := lookupName("agents", "name", a.params[0])
		return subject("agent", a.params[0], n), "Revoked agent " + n
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/agents/ca/rotate$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("agent", "", "agent CA"), "Started an agent CA rotation"
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/auth/logout$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("user", a.actor.ID, a.actor.Name), a.actor.Name + " signed out"
	}},
	{method: http.MethodDelete, pattern: regexp.MustCompile(`^/auth/sessions/([^/]+)$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("session", a.params[0], ""), a.actor.Name + " revoked a session"
	}},
	{method: http.MethodPost, pattern: regexp.MustCompile(`^/auth/sessions/revoke_others$`), describe: func(a auditCall) (events.Subject, string) {
		return subject("user", a.actor.ID, a.actor.Name), a.actor.Name + " revoked their other sessions"
	}},
}

// auditRecorder captures a response's status and, for the routes that
// create something, its body.
type auditRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *auditRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < 64<<10 {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Audit records administrative changes in the security log once they
// succeed: users, groups, memberships, roles, permissions, assignments, API
// keys, sign-outs, and sessions. Names come from the request or from a lookup
// before a delete; secrets never do, and a changed-field list leaves them out.
func Audit(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, prefix)
			var route *auditRoute
			var params []string
			for i := range auditRoutes {
				if auditRoutes[i].method != r.Method {
					continue
				}
				if m := auditRoutes[i].pattern.FindStringSubmatch(path); m != nil {
					route, params = &auditRoutes[i], m[1:]
					break
				}
			}
			if route == nil {
				next.ServeHTTP(w, r)
				return
			}
			var body map[string]any
			if r.Body != nil {
				raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				r.Body = io.NopCloser(bytes.NewReader(raw))
				_ = json.Unmarshal(raw, &body)
			}
			var before map[string]string
			if route.before != nil {
				before = route.before(params)
			}
			ctx, slot := events.WithActorSlot(r.Context())
			rec := &auditRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r.WithContext(ctx))
			if rec.status < 200 || rec.status >= 300 {
				return
			}
			var resp map[string]any
			_ = json.Unmarshal(rec.body.Bytes(), &resp)
			actor := *slot
			if !actor.Present() {
				actor = events.System()
			}
			subj, msg := route.describe(auditCall{params: params, body: body, resp: resp, before: before, actor: actor})
			events.Record(events.Entry{Category: models.EventSecurity, Subject: subj, Message: msg, Actor: actor})
		})
	}
}
