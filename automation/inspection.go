package automation

import (
	"context"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

type Element = autoapi.Element
type DisplayInfo = autoapi.DisplayInfo
type DisplayMode = autoapi.DisplayMode
type Inspection = autoapi.Inspection

type inspectionInvokeArgs struct {
	Window WindowInfo
	ID     string
}
type helperInspection struct{ h *helper }

func (i helperInspection) Elements(ctx context.Context, w WindowInfo) ([]Element, error) {
	var out []Element
	err := i.h.call(ctx, "inspection", "elements", w, &out)
	return out, err
}
func (i helperInspection) Invoke(ctx context.Context, w WindowInfo, id string) error {
	return i.h.callOnce(ctx, "inspection", "invoke", inspectionInvokeArgs{w, id}, nil)
}
func (i helperInspection) Displays(ctx context.Context) ([]DisplayInfo, error) {
	var out []DisplayInfo
	err := i.h.call(ctx, "inspection", "displays", nil, &out)
	return out, err
}
func (i helperInspection) SetDisplay(ctx context.Context, m DisplayMode) error {
	return i.h.callOnce(ctx, "inspection", "set_display", m, nil)
}
func serveInspection(ctx context.Context, s *Session, req hrpc.Request) (any, error) {
	switch req.Method {
	case "elements":
		var w WindowInfo
		if err := hrpc.Unmarshal(req.Args, &w); err != nil {
			return nil, err
		}
		return s.Inspection.Elements(ctx, w)
	case "invoke":
		var a inspectionInvokeArgs
		if err := hrpc.Unmarshal(req.Args, &a); err != nil {
			return nil, err
		}
		return nil, s.Inspection.Invoke(ctx, a.Window, a.ID)
	case "displays":
		return s.Inspection.Displays(ctx)
	case "set_display":
		var m DisplayMode
		if err := hrpc.Unmarshal(req.Args, &m); err != nil {
			return nil, err
		}
		return nil, s.Inspection.SetDisplay(ctx, m)
	}
	return nil, ErrUnsupported
}
