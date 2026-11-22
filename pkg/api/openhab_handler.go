package api

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/swaggest/usecase"
)

// OpenHABItemsHandler returns the interactor that lists a running openHAB
// connection's items for pickers, by floor and location.
func OpenHABItemsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[[]openhab.ItemView]) error {
		driver, err := connops.OpenHABDriver(in.ID)
		if err != nil {
			return err
		}
		if !driver.Connected() {
			return apperrors.ErrBadGateway.WithMessage("openHAB is not connected: " + driver.Status().Error)
		}
		out.Data = driver.Items().Views()
		return nil
	})
	u.SetTitle("List openHAB Items")
	u.SetDescription("Returns the items of a running openHAB connection with their state and location, ordered by floor and location.")
	u.SetTags("Connections")
	return u
}
