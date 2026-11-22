package notification

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/nikoksr/notify"
)

// activeSends reserves capacity until SDK work ends, including after caller timeout.
var activeSends = make(chan struct{}, 4)

// Driver owns immutable settings and cancels send contexts when retired.
type Driver struct {
	id      uuid.UUID
	cfg     Config
	mu      sync.Mutex
	status  integrations.Status
	ctx     context.Context
	cancel  context.CancelFunc
	publish func(integrations.Status)
}

// New validates settings without contacting a provider or allocating clients.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	c, err := decode(model.Config)
	if err != nil {
		return nil, err
	}
	if err = c.Validate(); err != nil {
		return nil, err
	}
	return &Driver{id: model.ID, cfg: c, publish: deps.PublishStatus}, nil
}
func (d *Driver) ID() uuid.UUID               { return d.id }
func (d *Driver) Kind() string                { return Kind }
func (d *Driver) Status() integrations.Status { d.mu.Lock(); defer d.mu.Unlock(); return d.status }

// Start reports configured readiness; actual provider authentication occurs on send.
func (d *Driver) Start(ctx context.Context) error {
	d.mu.Lock()
	d.ctx, d.cancel = context.WithCancel(ctx)
	d.status = integrations.Status{State: integrations.StateConnected, Since: time.Now()}
	status := d.status
	d.mu.Unlock()
	if d.publish != nil {
		d.publish(status)
	}
	return nil
}

// Stop revokes the driver's contexts without waiting for legacy SDK calls.
func (d *Driver) Stop() {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.status = integrations.Status{State: integrations.StateDisabled, Since: time.Now()}
	status := d.status
	d.mu.Unlock()
	if d.publish != nil {
		d.publish(status)
	}
}

// Endpoints returns only the IDs and labels authors may select.
func (d *Driver) Endpoints() []Endpoint {
	out := make([]Endpoint, 0, len(d.cfg.Endpoints))
	for _, e := range d.cfg.Endpoints {
		out = append(out, Endpoint{ID: e.ID, Label: e.Label})
	}
	return out
}

// DashboardAlert delivers an alert to a dashboard's open viewers. serve.go
// binds it to the runtime display store, which keeps this package free of
// dashboard imports.
var DashboardAlert func(dashboard, title, message string, settings map[string]string) error

// guardedNotifier contains SDK panics within notify's own send goroutine.
type guardedNotifier struct{ notify.Notifier }

func (n guardedNotifier) Send(ctx context.Context, subject, message string) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("provider operation stopped")
		}
	}()
	return n.Notifier.Send(ctx, subject, message)
}

// Send resolves an exact allowed ID before sender construction or network I/O.
func (d *Driver) Send(ctx context.Context, endpoint, subject, message string) error {
	if strings.ContainsAny(subject, "\r\n\x00") {
		return fmt.Errorf("notification subject must not contain line breaks or NUL")
	}
	if len(subject) > 4096 || len(message) > 65536 || message == "" {
		return fmt.Errorf("notification requires a message up to 64 KiB and subject up to 4 KiB")
	}
	var target string
	for _, e := range d.cfg.Endpoints {
		if e.ID == endpoint {
			target = e.Target
			break
		}
	}
	if target == "" {
		return fmt.Errorf("notification endpoint is not allowed; select an endpoint configured in settings")
	}
	d.mu.Lock()
	runCtx := d.ctx
	enabled := d.status.State == integrations.StateConnected
	d.mu.Unlock()
	if !enabled || runCtx == nil {
		return fmt.Errorf("notification service is disabled")
	}
	// A dashboard alert is an in-process call with nothing to retry or time
	// out, and its errors name a dashboard rather than a provider secret, so
	// they pass through unchanged.
	if d.cfg.Service == "dashboard" {
		if DashboardAlert == nil {
			return fmt.Errorf("dashboard alerts are not available in this process")
		}
		return DashboardAlert(target, subject, message, d.cfg.Credentials)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(runCtx, cancel)
	defer stop()
	select {
	case activeSends <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-activeSends
		return err
	}
	result := make(chan error, 1)
	go func() {
		defer func() { <-activeSends }()
		var err error
		defer func() {
			if recover() != nil {
				err = fmt.Errorf("notification provider operation stopped")
			}
			result <- err
		}()
		sender, closeSender, e := newNotifier(ctx, d.cfg.Service, d.cfg.Credentials, target)
		if e != nil {
			err = fmt.Errorf("notification provider setup failed; check service credentials and endpoint settings")
			return
		}
		if closeSender != nil {
			defer closeSender()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}
		if e = notify.NewWithServices(guardedNotifier{sender}).Send(ctx, subject, message); e != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = fmt.Errorf("notification delivery failed; check provider status, credentials and endpoint permissions")
			}
		}
	}()
	select {
	case err := <-result:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
