package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/spf13/viper"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// ThemeListData holds the list of all themes and the default theme identifier.
type ThemeListData struct {
	Themes       []*models.Theme `json:"themes" description:"List of themes."`
	DefaultTheme string          `json:"default_theme" description:"Package name of the default theme."`
}

// ThemeListHandler returns the interactor that lists all themes.
func ThemeListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[ThemeListData]) error {
		var themes []*models.Theme
		if err := db.DB.Order("name ASC").Find(&themes).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = ThemeListData{
			Themes:       themes,
			DefaultTheme: viper.GetString("theme.default"),
		}
		return nil
	})
	u.SetTitle("List Themes")
	u.SetDescription("Returns all themes ordered by name.")
	u.SetTags("Themes")
	return u
}

// ThemeGetInput identifies a theme to retrieve by ID.
type ThemeGetInput struct {
	ID uint `path:"id" description:"Theme ID."`
}

// ThemeGetHandler returns the interactor that retrieves a single theme by ID.
func ThemeGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ThemeGetInput, out *apiutil.Success[*models.Theme]) error {
		var theme models.Theme
		if err := db.DB.First(&theme, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("theme not found")
		}

		out.Data = &theme
		return nil
	})
	u.SetTitle("Get Theme")
	u.SetDescription("Returns a single theme by ID.")
	u.SetTags("Themes")
	return u
}

// ThemeCreateInput holds fields for registering a new theme.
type ThemeCreateInput struct {
	Name        string `json:"name" required:"true" minLength:"1" description:"Theme display name."`
	Description string `json:"description" description:"Optional theme description."`
	PackageName string `json:"package_name" required:"true" minLength:"1" description:"Package identifier for the theme."`
	Version     string `json:"version" description:"Theme version string."`
	Author      string `json:"author" description:"Theme author."`
	Variables   string `json:"variables" description:"JSON or CSS variable definitions."`
}

// ThemeCreateHandler returns the interactor that registers a new theme.
func ThemeCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ThemeCreateInput, out *apiutil.Success[*models.Theme]) error {
		// Build theme record.
		theme := &models.Theme{
			Name:        in.Name,
			Description: in.Description,
			PackageName: in.PackageName,
			Version:     in.Version,
			Author:      in.Author,
			Variables:   in.Variables,
		}

		// Persist to database.
		if err := db.DB.Create(theme).Error; err != nil {
			return duplicateOr(err, "a theme with this package name already exists")
		}

		// Build response.
		out.Data = theme
		return nil
	})
	u.SetTitle("Create Theme")
	u.SetDescription("Registers a new theme.")
	u.SetTags("Themes")
	return u
}

// ThemeUpdateInput holds the theme ID and optional fields for patching a theme.
type ThemeUpdateInput struct {
	ID          uint    `path:"id" description:"Theme ID."`
	Name        *string `json:"name,omitempty" minLength:"1" description:"Theme display name."`
	Description *string `json:"description,omitempty" description:"Theme description."`
	Version     *string `json:"version,omitempty" description:"Theme version string."`
	Author      *string `json:"author,omitempty" description:"Theme author."`
	Variables   *string `json:"variables,omitempty" description:"JSON or CSS variable definitions."`
}

// ThemeUpdateHandler returns the interactor that patches theme fields.
func ThemeUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ThemeUpdateInput, out *apiutil.Success[*models.Theme]) error {
		// Load existing theme.
		var theme models.Theme
		if err := db.DB.First(&theme, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("theme not found")
		}

		// Apply requested field updates.
		if in.Name != nil {
			theme.Name = *in.Name
		}
		if in.Description != nil {
			theme.Description = *in.Description
		}
		if in.Version != nil {
			theme.Version = *in.Version
		}
		if in.Author != nil {
			theme.Author = *in.Author
		}
		if in.Variables != nil {
			theme.Variables = *in.Variables
		}

		// Persist changes.
		if err := db.DB.Save(&theme).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = &theme
		return nil
	})
	u.SetTitle("Update Theme")
	u.SetDescription("Updates an existing theme by ID.")
	u.SetTags("Themes")
	return u
}

// ThemeDeleteInput identifies a theme to remove by ID.
type ThemeDeleteInput struct {
	ID uint `path:"id" description:"Theme ID."`
}

// ThemeDeleteHandler returns the interactor that removes a theme.
func ThemeDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ThemeDeleteInput, out *apiutil.Success[any]) error {
		res := db.DB.Delete(&models.Theme{}, in.ID)
		if res.Error != nil {
			return apperrors.ErrInternal.WithError(res.Error)
		}
		if res.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("theme not found")
		}

		out.Message = "Theme deleted"
		return nil
	})
	u.SetTitle("Delete Theme")
	u.SetDescription("Deletes a theme by ID.")
	u.SetTags("Themes")
	return u
}

// RegisterThemeRoutes registers theme CRUD endpoints.
func RegisterThemeRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Public theme reads.
	s.Method(http.MethodGet, "/themes",
		nethttp.NewHandler(ThemeListHandler()))
	s.Method(http.MethodGet, "/themes/{id}",
		nethttp.NewHandler(ThemeGetHandler()))

	// Permission-gated theme mutations.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermThemesWrite), bearerDoc).
		Method(http.MethodPost, "/themes",
			nethttp.NewHandler(ThemeCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermThemesWrite), bearerDoc).
		Method(http.MethodPut, "/themes/{id}",
			nethttp.NewHandler(ThemeUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermThemesDelete), bearerDoc).
		Method(http.MethodDelete, "/themes/{id}",
			nethttp.NewHandler(ThemeDeleteHandler()))
}
