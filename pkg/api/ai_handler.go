package api

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/swaggest/usecase"
)

// AIModelsInput identifies the AI connection to list models from, with an
// optional cache bypass.
type AIModelsInput struct {
	ID      string `path:"id" format:"uuid" description:"Connection identifier."`
	Refresh bool   `query:"refresh" description:"Bypass the cached catalog and list from the provider now."`
}

// AIModelsHandler returns the interactor that lists an AI provider
// connection's models for the editor's model autocomplete. The driver caches
// the catalog briefly, so opening the picker does not cost a provider call
// each time.
func AIModelsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AIModelsInput, out *apiutil.Success[[]string]) error {
		driver, err := connops.AIDriver(in.ID)
		if err != nil {
			return err
		}
		var models []string
		if in.Refresh {
			models, err = driver.RefreshModels(ctx)
		} else {
			models, err = driver.Models(ctx)
		}
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("listing models: " + err.Error())
		}
		out.Data = models
		return nil
	})
	u.SetTitle("List AI Models")
	u.SetDescription("Returns the model identifiers an AI provider connection serves, from a briefly cached catalog.")
	u.SetTags("Connections")
	return u
}
