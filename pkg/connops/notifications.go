package connops

import (
	"context"
	"time"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
)

// NotificationEndpoints lists author-facing IDs without exposing receiver targets.
func NotificationEndpoints(id string) ([]notification.Endpoint, error) {
	c, err := Load(id)
	if err != nil {
		return nil, err
	}
	if c.Kind != notification.Kind {
		return nil, apperrors.ErrValidation.WithMessage("connection is not a notification service")
	}
	raw, err := c.ConfigJSON()
	if err != nil {
		return nil, apperrors.ErrInternal.WithMessage("notification settings could not be decrypted")
	}
	endpoints, err := notification.EndpointList(raw)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	return endpoints, nil
}

// SendNotification shares enabled-state and allowlist checks across API and nodes.
func SendNotification(ctx context.Context, id, endpoint, subject, message string, seconds int) error {
	if seconds < 1 || seconds > 120 {
		return apperrors.ErrValidation.WithMessage("notification timeout must be between 1 and 120 whole seconds")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	c, err := Load(id)
	if err != nil {
		return err
	}
	if c.Kind != notification.Kind {
		return apperrors.ErrValidation.WithMessage("connection is not a notification service")
	}
	if !c.Enabled {
		return apperrors.ErrValidation.WithMessage("notification service is disabled")
	}
	if mgr == nil {
		return apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	value, ok := mgr.Get(c.ID)
	if !ok {
		return apperrors.ErrBadGateway.WithMessage("notification service is not running; check its settings")
	}
	driver, ok := value.(*notification.Driver)
	if !ok {
		return apperrors.ErrInternal.WithMessage("connection driver is not a notification service")
	}
	if err = driver.Send(ctx, endpoint, subject, message); err != nil {
		return apperrors.ErrBadGateway.WithMessage(err.Error())
	}
	return nil
}
