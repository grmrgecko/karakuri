package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
)

// remoteCtx is the plugin-side nodeapi.ExecContext: config and ports arrived
// with the request and answer locally; everything stateful calls back into
// the host keyed by the context handle, so run state, variables, the node
// store, expressions, Park, and the injectable clock all live where they
// always did.
type remoteCtx struct {
	srv    *server
	ctx    context.Context
	ctxID  string
	nodeID string
	cfg    []byte
	ports  flow.PortSet
	wired  map[string]bool
	log    *zap.Logger
}

// newRemoteCtx builds the proxy context for one execution or trigger arm.
func (s *server) newRemoteCtx(ctx context.Context, ctxID, nodeID string, cfgJSON, portsJSON []byte, wiredOutputs []string) (*remoteCtx, error) {
	if s.host == nil {
		return nil, fmt.Errorf("plugin is not initialized")
	}
	var ports flow.PortSet
	if len(portsJSON) > 0 {
		if err := json.Unmarshal(portsJSON, &ports); err != nil {
			return nil, fmt.Errorf("invalid port set: %w", err)
		}
	}
	wired := make(map[string]bool, len(wiredOutputs))
	for _, port := range wiredOutputs {
		wired[port] = true
	}
	return &remoteCtx{
		srv: s, ctx: ctx, ctxID: ctxID, nodeID: nodeID,
		cfg: cfgJSON, ports: ports, wired: wired,
		log: s.log.With(zap.String("node_id", nodeID)),
	}, nil
}

// decodeValue unwraps a ValueResponse into a decoded value and presence.
func decodeValue(resp *pb.ValueResponse, err error) (any, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if resp.Error != "" {
		return nil, false, fmt.Errorf("%s", resp.Error)
	}
	if !resp.Found {
		return nil, false, nil
	}
	var v any
	if len(resp.ValueJson) > 0 {
		if err := json.Unmarshal(resp.ValueJson, &v); err != nil {
			return nil, false, fmt.Errorf("host value cannot be decoded: %w", err)
		}
	}
	return v, true, nil
}

// encodeValue renders a value for a host callback.
func encodeValue(v any) []byte {
	enc, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return enc
}

func (c *remoteCtx) Config(out any) error {
	if len(c.cfg) == 0 {
		return nil
	}
	if err := json.Unmarshal(c.cfg, out); err != nil {
		return fmt.Errorf("invalid node config: %w", err)
	}
	return nil
}

func (c *remoteCtx) Logger() *zap.Logger { return c.log }

func (c *remoteCtx) Clock() nodeapi.Clock { return &remoteClock{c: c} }

func (c *remoteCtx) evalRequest(src string, msg *nodeapi.Msg) (*pb.EvalRequest, error) {
	req := &pb.EvalRequest{CtxId: c.ctxID, Src: src}
	if msg != nil {
		w, err := plugin.MsgToWire(msg)
		if err != nil {
			return nil, err
		}
		req.Msg = w
		req.HasMsg = true
	}
	return req, nil
}

func (c *remoteCtx) Eval(src string, msg *nodeapi.Msg) (any, error) {
	req, err := c.evalRequest(src, msg)
	if err != nil {
		return nil, err
	}
	v, _, err := decodeValue(c.srv.host.Eval(c.ctx, req))
	return v, err
}

func (c *remoteCtx) Interpolate(s string, msg *nodeapi.Msg) (string, error) {
	req, err := c.evalRequest(s, msg)
	if err != nil {
		return "", err
	}
	v, _, err := decodeValue(c.srv.host.Interpolate(c.ctx, req))
	if err != nil {
		return "", err
	}
	out, _ := v.(string)
	return out, nil
}

func (c *remoteCtx) NodeID() string { return c.nodeID }

func (c *remoteCtx) Ports() flow.PortSet { return c.ports }

func (c *remoteCtx) Wired(port string) bool { return c.wired[port] }

func (c *remoteCtx) Debug(payload any, note string) {
	_, err := c.srv.host.Debug(c.ctx, &pb.DebugRequest{
		CtxId: c.ctxID, PayloadJson: encodeValue(payload), Note: note,
	})
	if err != nil {
		c.log.Warn("debug callback failed", zap.Error(err))
	}
}

func (c *remoteCtx) GetState(key string) (any, bool) {
	v, ok, err := decodeValue(c.srv.host.GetState(c.ctx, &pb.CtxKey{CtxId: c.ctxID, Key: key}))
	if err != nil {
		c.log.Warn("state read failed", zap.Error(err))
		return nil, false
	}
	return v, ok
}

func (c *remoteCtx) SetState(key string, value any) {
	if _, err := c.srv.host.SetState(c.ctx, &pb.CtxSet{
		CtxId: c.ctxID, Key: key, ValueJson: encodeValue(value),
	}); err != nil {
		c.log.Warn("state write failed", zap.Error(err))
	}
}

func (c *remoteCtx) GetVar(key string) (any, bool) {
	v, ok, err := decodeValue(c.srv.host.GetVar(c.ctx, &pb.CtxKey{CtxId: c.ctxID, Key: key}))
	if err != nil {
		c.log.Warn("var read failed", zap.Error(err))
		return nil, false
	}
	return v, ok
}

func (c *remoteCtx) SetVar(key string, value any) {
	req := &pb.CtxSet{CtxId: c.ctxID, Key: key, ValueJson: encodeValue(value), Clear: value == nil}
	if _, err := c.srv.host.SetVar(c.ctx, req); err != nil {
		c.log.Warn("var write failed", zap.Error(err))
	}
}

func (c *remoteCtx) Vars() map[string]any {
	v, _, err := decodeValue(c.srv.host.Vars(c.ctx, &pb.CtxOnly{CtxId: c.ctxID}))
	if err != nil {
		c.log.Warn("vars snapshot failed", zap.Error(err))
		return map[string]any{}
	}
	out, _ := v.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func (c *remoteCtx) GetScoped(scope, key string) (any, bool) {
	v, ok, err := decodeValue(c.srv.host.GetScoped(c.ctx, &pb.ScopedKey{
		CtxId: c.ctxID, Scope: scope, Key: key,
	}))
	if err != nil {
		c.log.Warn("scoped read failed", zap.Error(err))
		return nil, false
	}
	return v, ok
}

func (c *remoteCtx) SetScoped(scope, key string, value any) error {
	resp, err := c.srv.host.SetScoped(c.ctx, &pb.ScopedSet{
		CtxId: c.ctxID, Scope: scope, Key: key,
		ValueJson: encodeValue(value), Clear: value == nil,
	})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (c *remoteCtx) ScopedVars(scope string) map[string]any {
	v, _, err := decodeValue(c.srv.host.ScopedVars(c.ctx, &pb.ScopedKey{CtxId: c.ctxID, Scope: scope}))
	if err != nil {
		c.log.Warn("scoped snapshot failed", zap.Error(err))
		return map[string]any{}
	}
	out, _ := v.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func (c *remoteCtx) NodeStore() nodeapi.NodeStore { return &remoteStore{c: c} }

func (c *remoteCtx) DurableStore() nodeapi.NodeStore { return &remoteStore{c: c, durable: true} }

func (c *remoteCtx) Park(ctx context.Context, d time.Duration) error {
	resp, err := c.srv.host.Park(ctx, &pb.WaitRequest{CtxId: c.ctxID, DurationMs: d.Milliseconds()})
	if err != nil {
		// The RPC context canceling is the park being interrupted, which is
		// how Park reports cancellation.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// Skipped records a canceled run for an event the trigger skipped. It is
// meaningful only for an armed trigger; the host ignores it elsewhere.
func (c *remoteCtx) Skipped(msg *nodeapi.Msg, reason string) {
	var w *pb.WireMsg
	if msg != nil {
		encoded, err := plugin.MsgToWire(msg)
		if err != nil {
			c.log.Error("skipped event cannot be encoded", zap.Error(err))
		} else {
			w = encoded
		}
	}
	if _, err := c.srv.host.TriggerSkipped(c.ctx, &pb.TriggerSkippedRequest{CtxId: c.ctxID, Msg: w, Reason: reason}); err != nil {
		c.log.Warn("skipped callback failed", zap.Error(err))
	}
}

func (c *remoteCtx) Emit(port string, msg *nodeapi.Msg) {
	w, err := plugin.MsgToWire(msg)
	if err != nil {
		c.log.Error("emit dropped an unencodable message", zap.Error(err))
		return
	}
	if _, err := c.srv.host.Emit(c.ctx, &pb.EmitRequest{CtxId: c.ctxID, Port: port, Msg: w}); err != nil {
		c.log.Warn("emit callback failed", zap.Error(err))
	}
}

// CallTool is not carried over the plugin wire: the built-in AI agent is the
// only caller, so a plugin node asking for it is refused rather than proxied.
func (c *remoteCtx) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*nodeapi.Msg, error) {
	return nil, fmt.Errorf("tool calls are not supported from a plugin")
}

func (c *remoteCtx) Connection(id string) (any, error) { return c.srv.connectionFor(id) }

// remoteClock proxies the host's injectable clock, so a fake clock in tests
// drives plugin nodes exactly like built-ins.
type remoteClock struct{ c *remoteCtx }

func (rc *remoteClock) Now() time.Time {
	v, _, err := decodeValue(rc.c.srv.host.ClockNow(rc.c.ctx, &pb.Empty{}))
	if err == nil {
		if s, ok := v.(string); ok {
			if t, perr := time.Parse(time.RFC3339Nano, s); perr == nil {
				return t
			}
		}
	}
	return time.Now()
}

func (rc *remoteClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		_, err := rc.c.srv.host.ClockAfter(rc.c.ctx, &pb.WaitRequest{
			CtxId: rc.c.ctxID, DurationMs: d.Milliseconds(),
		})
		if err == nil {
			ch <- time.Now()
		}
	}()
	return ch
}

// remoteStore proxies the node's flow-scoped store. Update cannot ship its
// closure across the boundary, so it runs plugin-side against a snapshot and
// commits with compare-and-swap, retried on conflict — the same atomicity the
// in-process store's lock provides, at CAS granularity. durable addresses the
// node's persistent store; the host picks per request.
type remoteStore struct {
	c       *remoteCtx
	durable bool
}

func (st *remoteStore) Get(key string) (any, bool) {
	v, ok, err := decodeValue(st.c.srv.host.StoreGet(st.c.ctx, &pb.CtxKey{
		CtxId: st.c.ctxID, Key: key, Durable: st.durable,
	}))
	if err != nil {
		st.c.log.Warn("store read failed", zap.Error(err))
		return nil, false
	}
	return v, ok
}

func (st *remoteStore) Set(key string, value any) {
	if _, err := st.c.srv.host.StoreSet(st.c.ctx, &pb.CtxSet{
		CtxId: st.c.ctxID, Key: key, ValueJson: encodeValue(value), Durable: st.durable,
	}); err != nil {
		st.c.log.Warn("store write failed", zap.Error(err))
	}
}

func (st *remoteStore) Delete(key string) {
	if _, err := st.c.srv.host.StoreDelete(st.c.ctx, &pb.CtxKey{
		CtxId: st.c.ctxID, Key: key, Durable: st.durable,
	}); err != nil {
		st.c.log.Warn("store delete failed", zap.Error(err))
	}
}

func (st *remoteStore) Update(key string, fn func(cur any, ok bool) any) any {
	for range 64 {
		cur, ok := st.Get(key)
		// The expected value is encoded before fn runs, because fn may change
		// a map or slice in cur in place and return it.
		expected := encodeValue(cur)
		next := fn(cur, ok)
		req := &pb.StoreCASRequest{
			CtxId: st.c.ctxID, Key: key, Durable: st.durable,
			ExpectedJson: expected, ExpectedFound: ok,
		}
		if next == nil {
			req.Delete = true
		} else {
			req.NewJson = encodeValue(next)
		}
		resp, err := st.c.srv.host.StoreCAS(st.c.ctx, req)
		if err != nil {
			st.c.log.Warn("store update failed", zap.Error(err))
			return next
		}
		if resp.Swapped {
			return next
		}
	}
	// Persistent contention past any plausible level: take the last word
	// rather than looping forever.
	cur, ok := st.Get(key)
	next := fn(cur, ok)
	if next == nil {
		st.Delete(key)
	} else {
		st.Set(key, next)
	}
	return next
}
