package mqttrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
)

// MaxTemplateBytes bounds a command template. A device payload is a document,
// not a file, and a cap keeps a runaway paste out of the expression evaluator.
const MaxTemplateBytes = 64 << 10

// PublishSpec is one outbound message body: either a literal payload, or a
// template rendered against the caller's variables. The expression language is
// the one flows use, so a dashboard row and a Template node are written alike.
type PublishSpec struct {
	Payload  string
	Template string
	Format   string
	Vars     map[string]any
}

// runCommandScript executes a command script and reads its return value. The
// script formats a payload and nothing more: it gets no network, and a budget
// short enough that a stuck script fails the press rather than hanging it.
// Unlike a device's payload script, a bare string return is the payload and
// returning nothing is an error, since a template exists to produce a body.
func runCommandScript(ctx context.Context, source string, vars map[string]any) (CommandMessage, error) {
	// The JavaScript runtime stops a script when its context ends.
	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	res, err := sandbox.JSRuntime{}.Run(ctx, sandbox.Request{
		Language: sandbox.LangJavaScript,
		Source:   CommandPreamble + source,
		Payload:  vars,
		Network:  sandbox.NetworkNone,
		Limits:   sandbox.Limits{Timeout: CommandTimeout}.Normalize(),
	})
	if err != nil {
		return CommandMessage{}, err
	}

	// A bare string is the payload; an object may also name a topic. Anything
	// else is encoded as JSON, which is what a script building a document for a
	// JSON device returns.
	switch v := res.Value.(type) {
	case nil:
		return CommandMessage{}, errors.New("command script returned nothing")
	case string:
		return CommandMessage{Payload: v}, nil
	case map[string]any:
		if raw, ok := v["payload"]; ok {
			out := CommandMessage{}
			if topic, ok := v["topic"].(string); ok {
				out.Topic = topic
			}
			if text, ok := raw.(string); ok {
				out.Payload = text
				return out, nil
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				return CommandMessage{}, fmt.Errorf("command script payload is not encodable: %w", err)
			}
			out.Payload = string(encoded)
			return out, nil
		}
	}
	encoded, err := json.Marshal(res.Value)
	if err != nil {
		return CommandMessage{}, fmt.Errorf("command script result is not encodable: %w", err)
	}
	return CommandMessage{Payload: string(encoded)}, nil
}

// RenderPublishBody resolves what to publish: the literal payload, or the
// template rendered against its variables. A JSON template is parsed first and
// filled field by field, so a placeholder keeps its own type and the stored
// template is always something an editor can lint (see expr.InterpolateDocument).
// A script returns its own payload and may name its own topic, which is the
// only case where the topic is not the one the binding configured.
func RenderPublishBody(ctx context.Context, spec PublishSpec) (CommandMessage, error) {
	if spec.Template == "" {
		return CommandMessage{Payload: spec.Payload}, nil
	}
	if spec.Payload != "" {
		return CommandMessage{}, apperrors.ErrValidation.WithMessage("send either payload or template, not both")
	}
	if len(spec.Template) > MaxTemplateBytes {
		return CommandMessage{}, apperrors.ErrValidation.WithMessage("template is too large to render")
	}

	env := spec.Vars
	if env == nil {
		env = map[string]any{}
	}

	switch spec.Format {
	case "script":
		out, err := runCommandScript(ctx, spec.Template, env)
		if err != nil {
			return CommandMessage{}, apperrors.ErrValidation.WithMessage("command script failed: " + err.Error())
		}
		return out, nil

	case "json":
		var document any
		if err := json.Unmarshal([]byte(spec.Template), &document); err != nil {
			return CommandMessage{}, apperrors.ErrValidation.WithMessage("template is not valid JSON: " + err.Error())
		}
		filled, err := expr.InterpolateDocument(document, env)
		if err != nil {
			return CommandMessage{}, apperrors.ErrValidation.WithMessage("template failed: " + err.Error())
		}
		body, err := json.Marshal(filled)
		if err != nil {
			return CommandMessage{}, apperrors.ErrValidation.WithMessage("rendered template is not encodable: " + err.Error())
		}
		return CommandMessage{Payload: string(body)}, nil

	default:
		body, err := expr.Interpolate(spec.Template, env)
		if err != nil {
			return CommandMessage{}, apperrors.ErrValidation.WithMessage("template failed: " + err.Error())
		}
		return CommandMessage{Payload: body}, nil
	}
}
