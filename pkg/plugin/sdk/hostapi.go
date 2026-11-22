package sdk

import (
	"context"
	"errors"

	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// hostAdapter implements plugin.HostAPI over the gRPC callback channel, so a
// plugin's Init reads settings and registers permissions, providers, and topic
// subscriptions with plain Go calls and never sees the wire.
type hostAdapter struct {
	client pb.KarakuriHostClient
}

func (a *hostAdapter) GetConfig(ctx context.Context, key string) (string, bool, error) {
	resp, err := a.client.GetConfig(ctx, &pb.ConfigKey{Key: key})
	if err != nil {
		return "", false, err
	}
	return resp.Value, resp.Found, nil
}

func (a *hostAdapter) SetConfig(ctx context.Context, key, value string) error {
	resp, err := a.client.SetConfig(ctx, &pb.ConfigSet{Key: key, Value: value})
	if err != nil {
		return err
	}
	return errFrom(resp)
}

func (a *hostAdapter) Publish(ctx context.Context, topic, msgType string, payload []byte) error {
	_, err := a.client.Publish(ctx, &pb.PublishRequest{
		Topic: topic, MsgType: msgType, Payload: payload,
	})
	return err
}

func (a *hostAdapter) Subscribe(ctx context.Context, topic string) error {
	resp, err := a.client.Subscribe(ctx, &pb.TopicRequest{Topic: topic})
	if err != nil {
		return err
	}
	return errFrom(resp)
}

func (a *hostAdapter) Unsubscribe(ctx context.Context, topic string) error {
	_, err := a.client.Unsubscribe(ctx, &pb.TopicRequest{Topic: topic})
	return err
}

func (a *hostAdapter) RegisterPermission(ctx context.Context, permission string, actions []string, description string) error {
	_, err := a.client.RegisterPermission(ctx, &pb.PermissionRequest{
		Permission: permission, Actions: actions, Description: description,
	})
	return err
}

func (a *hostAdapter) UnregisterPermission(ctx context.Context, permission string) error {
	_, err := a.client.UnregisterPermission(ctx, &pb.PermissionName{Permission: permission})
	return err
}

func (a *hostAdapter) SeedRoleDefaults(ctx context.Context, defaults []plugin.RoleDefault) error {
	wire := make([]*pb.RoleDefault, len(defaults))
	for i, d := range defaults {
		wire[i] = &pb.RoleDefault{Permission: d.Permission, Roles: d.Roles, AllRoles: d.AllRoles}
	}
	resp, err := a.client.SeedRoleDefaults(ctx, &pb.SeedRoleDefaultsRequest{Defaults: wire})
	if err != nil {
		return err
	}
	return errFrom(resp)
}

func (a *hostAdapter) CheckPermission(ctx context.Context, userID, permission string) (bool, error) {
	resp, err := a.client.CheckPermission(ctx, &pb.PermissionCheck{
		UserId: userID, Permission: permission,
	})
	if err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (a *hostAdapter) ConnectionAccessToken(ctx context.Context, connectionID string) (string, error) {
	resp, err := a.client.ConnectionAccessToken(ctx, &pb.ConnectionTokenRequest{ConnId: connectionID})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Token, nil
}

func (a *hostAdapter) CheckDashboardAccess(ctx context.Context, userID, dashboardID, level string) (bool, error) {
	resp, err := a.client.CheckDashboardAccess(ctx, &pb.DashboardAccessCheck{
		UserId: userID, DashboardId: dashboardID, Level: level,
	})
	if err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (a *hostAdapter) CheckConnectionAccess(ctx context.Context, userID, connectionID string) (bool, error) {
	resp, err := a.client.CheckConnectionAccess(ctx, &pb.ConnectionAccessCheck{
		UserId: userID, ConnectionId: connectionID,
	})
	if err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (a *hostAdapter) RegisterProvider(ctx context.Context, providerType, name, endpoint, displayName string) error {
	_, err := a.client.RegisterProvider(ctx, &pb.ProviderRequest{
		Type: providerType, Name: name, Endpoint: endpoint, DisplayName: displayName,
	})
	return err
}

func (a *hostAdapter) UnregisterProvider(ctx context.Context, providerType, name string) error {
	_, err := a.client.UnregisterProvider(ctx, &pb.ProviderName{Type: providerType, Name: name})
	return err
}

func (a *hostAdapter) RegisterTopicPermission(ctx context.Context, pattern, permission string) error {
	_, err := a.client.RegisterTopicPermission(ctx, &pb.TopicPermissionRequest{
		Pattern: pattern, Permission: permission,
	})
	return err
}

func (a *hostAdapter) UnregisterTopicPermission(ctx context.Context, pattern string) error {
	_, err := a.client.UnregisterTopicPermission(ctx, &pb.TopicPermissionName{Pattern: pattern})
	return err
}

// errFrom turns the host's in-band error string back into an error, so a
// failure the host reported reads the same as a transport failure.
func errFrom(resp *pb.ErrorResponse) error {
	if resp == nil || resp.Error == "" {
		return nil
	}
	return errors.New(resp.Error)
}
