package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// TokenCmd groups API bearer-token commands.
type TokenCmd struct {
	Create TokenCreateCmd `cmd:"" help:"Create an API bearer token."`
	List   TokenListCmd   `cmd:"" help:"List API bearer tokens."`
}

// TokenCreateCmd creates a recoverable API key for a user.
type TokenCreateCmd struct {
	Username  string `arg:"" help:"Username that owns the token."`
	Name      string `arg:"" help:"Display name for the token."`
	ExpiresAt string `help:"Expiration time in RFC3339 format."`
}

// Run creates the token and prints its raw bearer value.
func (c *TokenCreateCmd) Run() error {
	if err := initDatabaseQuiet(); err != nil {
		return err
	}
	user, err := tokenUser(c.Username)
	if err != nil {
		return err
	}

	key := &models.APIKey{UserID: user.ID, Name: c.Name}
	if c.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, c.ExpiresAt)
		if err != nil {
			return fmt.Errorf("invalid expiration time: %w", err)
		}
		key.ExpiresAt = &expiresAt
	}
	raw, err := key.Generate()
	if err != nil {
		return err
	}
	if err := db.DB.Create(key).Error; err != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}
	fmt.Println(raw)
	return nil
}

// TokenListCmd lists recoverable API keys for a user.
type TokenListCmd struct {
	Username string `arg:"" help:"Username that owns the tokens."`
	Name     string `help:"Return only tokens with this display name."`
	Raw      bool   `help:"Print only raw bearer-token values."`
}

// Run prints the user's tokens, decrypting values available to this server.
func (c *TokenListCmd) Run() error {
	if err := initDatabaseQuiet(); err != nil {
		return err
	}
	user, err := tokenUser(c.Username)
	if err != nil {
		return err
	}

	query := db.DB.Where("user_id = ?", user.ID).Order("created_at DESC")
	if c.Name != "" {
		query = query.Where("name = ?", c.Name)
	}
	var keys []*models.APIKey
	if err := query.Find(&keys).Error; err != nil {
		return fmt.Errorf("failed to list tokens: %w", err)
	}

	if c.Raw {
		var unavailable *models.APIKey
		printed := 0
		for _, key := range keys {
			raw, err := key.Raw()
			if errors.Is(err, models.ErrAPIKeyNotRecoverable) {
				unavailable = key
				continue
			}
			if err != nil {
				return tokenReadError(key, err)
			}
			fmt.Println(raw)
			printed++
		}
		if printed == 0 && unavailable != nil {
			return tokenReadError(unavailable, models.ErrAPIKeyNotRecoverable)
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPREFIX\tTOKEN\tEXPIRES")
	for _, key := range keys {
		raw, err := key.Raw()
		if errors.Is(err, models.ErrAPIKeyNotRecoverable) {
			raw = "unavailable"
		} else if err != nil {
			return tokenReadError(key, err)
		}
		expires := "never"
		if key.ExpiresAt != nil {
			expires = key.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", key.ID, key.Name, key.Prefix, raw, expires)
	}
	return w.Flush()
}

// tokenUser resolves an exact username for token commands.
func tokenUser(username string) (*models.User, error) {
	var user models.User
	if err := db.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("user %q not found: %w", username, err)
	}
	return &user, nil
}

// tokenReadError adds the key identity and recovery action to a read failure.
func tokenReadError(key *models.APIKey, err error) error {
	if errors.Is(err, models.ErrAPIKeyNotRecoverable) {
		return fmt.Errorf("token %q predates recoverable storage; create a replacement", key.Name)
	}
	return fmt.Errorf("failed to read token %q: %w", key.Name, err)
}
