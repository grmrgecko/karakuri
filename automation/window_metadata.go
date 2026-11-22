package automation

import "context"

type metadataWindow struct {
	Window
	enrich func(context.Context, WindowInfo) WindowInfo
}

func (w metadataWindow) List(ctx context.Context) ([]WindowInfo, error) {
	list, err := w.Window.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		list[i] = w.enrich(ctx, list[i])
	}
	return list, nil
}
