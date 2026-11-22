package core

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// NotificationNode sends once to an endpoint approved in Settings.
type NotificationNode struct{}

func (NotificationNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "notification", Label: "Send notification", Folder: flow.FolderNetwork, Icon: "las la-bell", Description: "Sends a notification to an allowed endpoint configured in Settings",
		ConnectionKind: notification.Kind, Inputs: []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}, Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		ConfigSpec: []engine.FieldSpec{
			{ID: "connection", Label: "Notification service", Type: flow.FieldConnection, ConnectionKind: notification.Kind, Required: true, Help: "Configure services and allowed endpoints in Settings → Notifications."},
			{ID: "endpoint", Label: "Allowed endpoint", Type: flow.FieldString, Control: "notification-endpoint", Required: true, Help: "One endpoint ID configured on the selected service. URLs or recipient IDs cannot replace it."},
			{ID: "subject", Label: "Subject", Type: flow.FieldString, Help: "Supports {{ }} expressions against the incoming message."},
			{ID: "message", Label: "Message", Type: flow.FieldText, Required: true, Placeholder: "{{ payload }}", Help: "Notification body, with {{ }} expressions. Up to 64 KiB."},
			{ID: "timeout", Label: "Timeout (seconds)", Type: flow.FieldNumber, Default: 30, Help: "Whole seconds from 1 to 120. Delivery may finish after a timeout with some legacy providers; failures never retry automatically."},
		}}
}

// Execute preserves the message and marks every delivery failure nonretryable.
func (NotificationNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (outputs map[string][]*engine.Msg, err error) {
	defer func() {
		if err != nil {
			err = &nodeapi.ExecutionError{Message: err.Error(), Cause: err, Retryable: false}
		}
	}()
	var cfg struct {
		Connection string `json:"connection"`
		Endpoint   string `json:"endpoint"`
		Subject    string `json:"subject"`
		Message    string `json:"message"`
		Timeout    *int   `json:"timeout"`
	}
	if err = ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	subject, err := ec.Interpolate(cfg.Subject, msg)
	if err != nil {
		return nil, err
	}
	message, err := ec.Interpolate(cfg.Message, msg)
	if err != nil {
		return nil, err
	}
	seconds := 30
	if cfg.Timeout != nil {
		seconds = *cfg.Timeout
	}
	if err = connops.SendNotification(ctx, cfg.Connection, cfg.Endpoint, subject, message, seconds); err != nil {
		return nil, err
	}
	return engine.One("out", passThrough(msg)), nil
}
