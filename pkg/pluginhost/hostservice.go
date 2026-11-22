package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// hostService serves the plugin-to-host callback channel: every stateful
// ExecContext method a remote node calls lands here, resolved through a
// handle to the real context the engine built. State, variables, the node
// store, expressions, Park, and the clock therefore behave identically for
// plugin nodes and built-ins.
type hostService struct {
	pb.UnimplementedKarakuriHostServer

	mu   sync.Mutex
	ctxs map[string]engine.ExecContext
}

func newHostService() *hostService {
	return &hostService{ctxs: make(map[string]engine.ExecContext)}
}

// register stores a live ExecContext and returns its handle.
func (h *hostService) register(ec engine.ExecContext) string {
	id := uuid.NewString()
	h.mu.Lock()
	h.ctxs[id] = ec
	h.mu.Unlock()
	return id
}

// release drops a handle once its execution finished.
func (h *hostService) release(id string) {
	h.mu.Lock()
	delete(h.ctxs, id)
	h.mu.Unlock()
}

// resolve returns the ExecContext behind a handle.
func (h *hostService) resolve(id string) (engine.ExecContext, error) {
	h.mu.Lock()
	ec, ok := h.ctxs[id]
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown execution context %q", id)
	}
	return ec, nil
}

// valueOK wraps a present value for the wire.
func valueOK(v any) *pb.ValueResponse {
	enc, err := json.Marshal(v)
	if err != nil {
		return &pb.ValueResponse{Error: err.Error()}
	}
	return &pb.ValueResponse{ValueJson: enc, Found: true}
}

func valueMissing() *pb.ValueResponse { return &pb.ValueResponse{} }

func valueErr(err error) *pb.ValueResponse { return &pb.ValueResponse{Error: err.Error()} }

func (h *hostService) evalMsg(req *pb.EvalRequest) (*engine.Msg, error) {
	if !req.HasMsg {
		return nil, nil
	}
	return plugin.MsgFromWire(req.Msg)
}

func (h *hostService) Eval(ctx context.Context, req *pb.EvalRequest) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	msg, err := h.evalMsg(req)
	if err != nil {
		return valueErr(err), nil
	}
	v, err := ec.Eval(req.Src, msg)
	if err != nil {
		return valueErr(err), nil
	}
	return valueOK(v), nil
}

func (h *hostService) Interpolate(ctx context.Context, req *pb.EvalRequest) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	msg, err := h.evalMsg(req)
	if err != nil {
		return valueErr(err), nil
	}
	s, err := ec.Interpolate(req.Src, msg)
	if err != nil {
		return valueErr(err), nil
	}
	return valueOK(s), nil
}

func (h *hostService) Debug(ctx context.Context, req *pb.DebugRequest) (*pb.Empty, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.Empty{}, nil
	}
	var payload any
	if len(req.PayloadJson) > 0 {
		_ = json.Unmarshal(req.PayloadJson, &payload)
	}
	ec.Debug(payload, req.Note)
	return &pb.Empty{}, nil
}

func decodeSet(raw []byte) any {
	var v any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

func (h *hostService) GetState(ctx context.Context, req *pb.CtxKey) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	v, ok := ec.GetState(req.Key)
	if !ok {
		return valueMissing(), nil
	}
	return valueOK(v), nil
}

func (h *hostService) SetState(ctx context.Context, req *pb.CtxSet) (*pb.Empty, error) {
	if ec, err := h.resolve(req.CtxId); err == nil {
		ec.SetState(req.Key, decodeSet(req.ValueJson))
	}
	return &pb.Empty{}, nil
}

func (h *hostService) GetVar(ctx context.Context, req *pb.CtxKey) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	v, ok := ec.GetVar(req.Key)
	if !ok {
		return valueMissing(), nil
	}
	return valueOK(v), nil
}

func (h *hostService) SetVar(ctx context.Context, req *pb.CtxSet) (*pb.Empty, error) {
	if ec, err := h.resolve(req.CtxId); err == nil {
		if req.Clear {
			ec.SetVar(req.Key, nil)
		} else {
			ec.SetVar(req.Key, decodeSet(req.ValueJson))
		}
	}
	return &pb.Empty{}, nil
}

func (h *hostService) Vars(ctx context.Context, req *pb.CtxOnly) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	return valueOK(ec.Vars()), nil
}

func (h *hostService) GetScoped(ctx context.Context, req *pb.ScopedKey) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	v, ok := ec.GetScoped(req.Scope, req.Key)
	if !ok {
		return valueMissing(), nil
	}
	return valueOK(v), nil
}

func (h *hostService) SetScoped(ctx context.Context, req *pb.ScopedSet) (*pb.ErrorResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	value := any(nil)
	if !req.Clear {
		value = decodeSet(req.ValueJson)
	}
	if err := ec.SetScoped(req.Scope, req.Key, value); err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	return &pb.ErrorResponse{}, nil
}

func (h *hostService) ScopedVars(ctx context.Context, req *pb.ScopedKey) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	return valueOK(ec.ScopedVars(req.Scope)), nil
}

// storeFor picks the memory-only or durable store a request addresses.
func storeFor(ec engine.ExecContext, durable bool) engine.NodeStore {
	if durable {
		return ec.DurableStore()
	}
	return ec.NodeStore()
}

func (h *hostService) StoreGet(ctx context.Context, req *pb.CtxKey) (*pb.ValueResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return valueErr(err), nil
	}
	v, ok := storeFor(ec, req.Durable).Get(req.Key)
	if !ok {
		return valueMissing(), nil
	}
	return valueOK(v), nil
}

func (h *hostService) StoreSet(ctx context.Context, req *pb.CtxSet) (*pb.Empty, error) {
	if ec, err := h.resolve(req.CtxId); err == nil {
		storeFor(ec, req.Durable).Set(req.Key, decodeSet(req.ValueJson))
	}
	return &pb.Empty{}, nil
}

func (h *hostService) StoreDelete(ctx context.Context, req *pb.CtxKey) (*pb.Empty, error) {
	if ec, err := h.resolve(req.CtxId); err == nil {
		storeFor(ec, req.Durable).Delete(req.Key)
	}
	return &pb.Empty{}, nil
}

// StoreCAS is the remote half of NodeStore.Update: the swap happens inside
// the real store's Update, so the compare and the write are atomic under the
// store lock. Values compare by their canonical JSON, the same form every
// value in this system round-trips through.
func (h *hostService) StoreCAS(ctx context.Context, req *pb.StoreCASRequest) (*pb.StoreCASResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.StoreCASResponse{}, nil
	}
	resp := &pb.StoreCASResponse{}
	storeFor(ec, req.Durable).Update(req.Key, func(cur any, ok bool) any {
		curJSON, _ := json.Marshal(cur)
		if ok != req.ExpectedFound || (ok && !bytes.Equal(curJSON, req.ExpectedJson)) {
			// Conflict: keep whatever is there and report the current value.
			resp.CurrentFound = ok
			resp.CurrentJson = curJSON
			if !ok {
				return nil
			}
			return cur
		}
		resp.Swapped = true
		if req.Delete {
			return nil
		}
		return decodeSet(req.NewJson)
	})
	return resp, nil
}

func (h *hostService) Park(ctx context.Context, req *pb.WaitRequest) (*pb.ErrorResponse, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	if err := ec.Park(ctx, time.Duration(req.DurationMs)*time.Millisecond); err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	return &pb.ErrorResponse{}, nil
}

func (h *hostService) ClockNow(ctx context.Context, _ *pb.Empty) (*pb.ValueResponse, error) {
	// Any live context answers; the clock is engine-wide. With none live the
	// caller falls back to its own clock.
	h.mu.Lock()
	var ec engine.ExecContext
	for _, c := range h.ctxs {
		ec = c
		break
	}
	h.mu.Unlock()
	if ec == nil {
		return valueErr(fmt.Errorf("no live execution context")), nil
	}
	return valueOK(ec.Clock().Now().Format(time.RFC3339Nano)), nil
}

// ClockAfter blocks until the host clock fires or the call is canceled, which
// is what lets a fake clock in tests drive plugin nodes.
func (h *hostService) ClockAfter(ctx context.Context, req *pb.WaitRequest) (*pb.Empty, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.Empty{}, err
	}
	select {
	case <-ec.Clock().After(time.Duration(req.DurationMs) * time.Millisecond):
		return &pb.Empty{}, nil
	case <-ctx.Done():
		return &pb.Empty{}, ctx.Err()
	}
}

func (h *hostService) Emit(ctx context.Context, req *pb.EmitRequest) (*pb.Empty, error) {
	ec, err := h.resolve(req.CtxId)
	if err != nil {
		return &pb.Empty{}, nil
	}
	msg, err := plugin.MsgFromWire(req.Msg)
	if err != nil {
		return &pb.Empty{}, nil
	}
	ec.Emit(req.Port, msg)
	return &pb.Empty{}, nil
}
