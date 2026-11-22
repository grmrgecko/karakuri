package api

import (
	"context"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/swaggest/usecase"
)

// MQTTPublishInput is one outbound MQTT message. The body is either given
// literally as payload or rendered here from the caller's own template and
// variables; either way the caller decides what the device sees.
type MQTTPublishInput struct {
	ID      string `path:"id" format:"uuid" description:"Connection UUID."`
	Topic   string `json:"topic" required:"true" minLength:"1" description:"Topic to publish on."`
	Payload string `json:"payload,omitempty" description:"Message body, published as UTF-8 text. Omit when sending a template."`
	// Template and Vars are the rendered form. The expression language is the
	// one flows use, so a dashboard row and a Template node are written alike.
	Template string         `json:"template,omitempty" description:"Body template with {{ }} expressions, rendered against vars."`
	Format   string         `json:"template_format,omitempty" enum:"text,json,script" description:"How the template is read: text renders it as written, json fills a JSON document field by field, script runs it as JavaScript."`
	Vars     map[string]any `json:"vars,omitempty" description:"Values the template's expressions read."`
	QoS      int            `json:"qos,omitempty" maximum:"2" minimum:"0" description:"Delivery quality of service, 0 to 2."`
	Retain   bool           `json:"retain,omitempty" description:"Whether the broker retains the message for later subscribers."`
}

// MQTTPublishHandler returns the interactor that publishes one message. This is
// the control path for dashboard rows bound to an MQTT device: the row names a
// command topic and the payload the device expects, exactly as it would be
// written in the device's own documentation. Nothing here decides what a
// device's payload should look like — a template only puts the caller's own
// values into the caller's own document.
func MQTTPublishHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTPublishInput, out *apiutil.Success[struct{}]) error {
		driver, err := connops.MQTTDriver(in.ID)
		if err != nil {
			return err
		}
		topic := strings.TrimSpace(in.Topic)
		if topic == "" {
			return apperrors.ErrValidation.WithMessage("topic is required")
		}
		if in.QoS < 0 || in.QoS > 2 {
			return apperrors.ErrValidation.WithMessage("qos must be 0, 1, or 2")
		}

		msg, err := mqttrelay.RenderPublishBody(ctx, mqttrelay.PublishSpec{
			Payload:  in.Payload,
			Template: in.Template,
			Format:   in.Format,
			Vars:     in.Vars,
		})
		if err != nil {
			return err
		}
		// A script may route its own message; anything else goes where the
		// binding said.
		if chosen := strings.TrimSpace(msg.Topic); chosen != "" {
			topic = chosen
		}

		if err := driver.Publish(ctx, topic, byte(in.QoS), in.Retain, []byte(msg.Payload)); err != nil {
			return apperrors.ErrBadGateway.WithMessage(err.Error())
		}
		out.Message = "Published"
		return nil
	})
	u.SetTitle("Publish MQTT Message")
	u.SetDescription("Publishes one message through a running MQTT connection, the command path for dashboard rows.")
	u.SetTags("Connections")
	return u
}
