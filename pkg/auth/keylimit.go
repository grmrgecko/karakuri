package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/permissions"
)

// MaxKeyPermissions bounds how many permission strings one API key may list.
const MaxKeyPermissions = 64

type keyLimitKey struct{}

// keyLimit is the permission limit of the API key a request authenticated
// with. A request without one, a session or a background job, is limited only
// by its user's roles.
type keyLimit struct {
	permissions []string
}

// WithKeyLimit returns a context whose permission checks are also limited to
// the given permission strings, as a limited API key's are.
func WithKeyLimit(ctx context.Context, perms []string) context.Context {
	return context.WithValue(ctx, keyLimitKey{}, keyLimit{permissions: append([]string(nil), perms...)})
}

// KeyLimit returns the permission strings a request's API key is limited to,
// and whether it is limited at all.
func KeyLimit(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	l, ok := ctx.Value(keyLimitKey{}).(keyLimit)
	if !ok {
		return nil, false
	}
	return l.permissions, true
}

// Can reports whether a request may use a permission: its user's roles grant
// it and, when it authenticated with a limited API key, one of the key's
// strings matches it. Every permission check made on behalf of a request goes
// through here, so a key's limit holds below the route as well as at it.
func Can(ctx context.Context, userID uuid.UUID, required string) bool {
	if limit, ok := KeyLimit(ctx); ok && !matchesAny(limit, required) {
		return false
	}
	return HasPermission(userID, required)
}

// Covers reports whether a request may hand a permission string, possibly a
// wildcard, to a new API key: its user's roles must hold a pattern at least as
// broad, and so must the limit of the key it authenticated with, if any.
func Covers(ctx context.Context, userID uuid.UUID, pattern string) bool {
	if limit, ok := KeyLimit(ctx); ok && !matchesAny(limit, pattern) {
		return false
	}
	held, err := GetEffectivePermissions(userID)
	if err != nil {
		return false
	}
	return matchesAny(held, pattern)
}

// EffectiveFor returns the registered permissions a request may use, for
// offering them as choices when a key is limited.
func EffectiveFor(ctx context.Context, userID uuid.UUID) []string {
	var out []string
	for _, def := range permissions.R.All() {
		for _, action := range def.Actions {
			p := def.Permission + ":" + action
			if Can(ctx, userID, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// ValidateKeyPermissions checks the shape of a key's permission strings: the
// segments of the role syntax, each a name or a wildcard.
func ValidateKeyPermissions(perms []string) error {
	if len(perms) > MaxKeyPermissions {
		return fmt.Errorf("a key holds at most %d permissions", MaxKeyPermissions)
	}
	for _, p := range perms {
		if p == "" || len(p) > 200 {
			return fmt.Errorf("permission %q must be 1 to 200 bytes", p)
		}
		for _, part := range strings.Split(p, ":") {
			if part == "" {
				return fmt.Errorf("permission %q has an empty segment", p)
			}
			if part == "*" {
				continue
			}
			for _, r := range part {
				if !(r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
					return fmt.Errorf("permission %q holds %q", p, r)
				}
			}
		}
	}
	return nil
}

func matchesAny(held []string, required string) bool {
	for _, h := range held {
		if permissions.MatchPermission(h, required) {
			return true
		}
	}
	return false
}
