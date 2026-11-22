package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/access"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
	"github.com/grmrgecko/karakuri/pkg/widgets"
)

// loadTimeout bounds each step of bringing a plugin process up.
const loadTimeout = 10 * time.Second

// Manager owns the plugin lifecycle: discovery, applying a plugin's
// contributions, reversing them, and remembering the operator's enable choice
// across restarts. One bad plugin logs and is skipped, never preventing
// startup.
//
// "Loading" means applying contributions, which is not the same as starting a
// process: a plugin that ships only widget packages loads and unloads with no
// subprocess at all, and enable/disable behaves identically for both kinds.
type Manager struct {
	host *Host
	log  *zap.Logger
	svc  *hostService

	mu sync.Mutex
	// found is every plugin discovered on disk, by name; loaded is the subset
	// whose contributions are currently applied.
	found  map[string]ExternalManifest
	loaded map[string]*loadedPlugin
	// loading marks plugins whose process is starting, so a second load of
	// the same name during those seconds is refused rather than started twice.
	loading map[string]bool

	stop chan struct{}
	wg   sync.WaitGroup
}

// loadedPlugin is one plugin whose contributions are applied. client and rpc
// are nil for a plugin that ships no executable.
type loadedPlugin struct {
	manifest ExternalManifest
	// builtin marks a directory that belongs to a compiled-in plugin: its
	// widgets loaded, but the plugin itself is not the manager's to stop.
	builtin bool
	client  *goplugin.Client
	rpc     pb.KarakuriPluginClient
	host    *pluginHost
	// settingsSchema is the JSON Schema the plugin reported at load, empty
	// when it has no settings.
	settingsSchema string
}

// NewManager creates a plugin manager registering through host.
func NewManager(host *Host, log *zap.Logger) *Manager {
	if log == nil {
		log = zap.NewNop()
	}
	return &Manager{
		host:    host,
		log:     log,
		svc:     newHostService(),
		found:   make(map[string]ExternalManifest),
		loaded:  make(map[string]*loadedPlugin),
		loading: make(map[string]bool),
		stop:    make(chan struct{}),
	}
}

// LoadAll discovers plugins under the given paths, reconciles them against
// stored state, and loads every enabled one. Individual failures are recorded
// on the plugin's row and skipped, and a directory the scan refuses is logged
// and left out.
func (m *Manager) LoadAll(paths []string) error {
	manifests, scanErr := DiscoverExternal(paths)
	if scanErr != nil {
		m.log.Error("skipping plugin directories that could not be read", zap.Error(scanErr))
	}

	known := make(map[string]bool, len(manifests))
	m.mu.Lock()
	m.found = make(map[string]ExternalManifest, len(manifests))
	for _, mf := range manifests {
		m.found[mf.Name] = mf
		known[mf.Name] = true
	}
	m.mu.Unlock()

	for _, mf := range manifests {
		// A compiled-in plugin's directory is loaded unconditionally: there is
		// no enable choice to remember for something that cannot be unloaded.
		if m.host.IsBuiltin(mf.Name) {
			if err := m.LoadPlugin(mf.Name); err != nil {
				m.log.Error("failed to load built-in plugin resources",
					zap.String("plugin", mf.Name), zap.Error(err))
			}
			continue
		}

		state := m.reconcileState(mf)
		if state != nil && !state.Enabled {
			m.log.Info("skipping disabled plugin", zap.String("plugin", mf.Name))
			continue
		}
		if err := m.LoadPlugin(mf.Name); err != nil {
			m.log.Error("failed to load plugin",
				zap.String("plugin", mf.Name), zap.String("dir", mf.Dir), zap.Error(err))
		}
	}

	// Forget plugins that are gone from disk, along with the widget rows and
	// settings they left behind. A plugin that merely failed to load is still
	// known, so a bad build does not discard an operator's configuration. A
	// directory the scan could not read may belong to any stored plugin, so
	// nothing is forgotten until a scan reads them all.
	if scanErr == nil {
		m.pruneStale(known)
		widgets.PruneOrphans(m.log, known)
	}

	m.wg.Add(1)
	go m.livenessLoop()
	return nil
}

// reconcileState creates or refreshes a plugin's stored row from its manifest
// and returns it. A first-seen plugin starts from the manifest's enable
// default; after that the stored choice is the operator's and the manifest no
// longer moves it. A host running without a database — the plugin tests do —
// gets a nil row and every plugin treated as enabled.
func (m *Manager) reconcileState(mf ExternalManifest) *models.PluginState {
	if db.DB == nil {
		return nil
	}
	state, err := models.GetPluginState(mf.Name)
	if err != nil {
		state = &models.PluginState{Name: mf.Name, Enabled: !mf.DefaultDisabled, Status: models.PluginStatusUnloaded}
	}
	state.Path = mf.Dir
	state.Version = mf.Version
	state.Description = mf.Description
	state.Author = mf.Author
	if err := models.SavePluginState(state); err != nil {
		m.log.Warn("failed to record plugin state", zap.String("plugin", mf.Name), zap.Error(err))
	}
	return state
}

// pruneStale drops stored state and settings for plugins no longer on disk.
func (m *Manager) pruneStale(known map[string]bool) {
	if db.DB == nil {
		return
	}
	states, err := models.ListPluginStates()
	if err != nil {
		m.log.Warn("failed to list plugin states", zap.Error(err))
		return
	}
	for _, state := range states {
		if known[state.Name] {
			continue
		}
		m.log.Info("removing state for a plugin no longer on disk", zap.String("plugin", state.Name))
		if err := models.DeletePluginState(state.Name); err != nil {
			m.log.Warn("failed to delete plugin state", zap.String("plugin", state.Name), zap.Error(err))
		}
		if err := models.DeleteSettingsByPrefix("plugins." + state.Name + "."); err != nil {
			m.log.Warn("failed to delete plugin settings", zap.String("plugin", state.Name), zap.Error(err))
		}
	}
}

// Load applies one discovered plugin by manifest. It is the entry point tests
// use; the API goes through LoadPlugin by name.
func (m *Manager) Load(mf ExternalManifest) error {
	m.mu.Lock()
	m.found[mf.Name] = mf
	m.mu.Unlock()
	return m.LoadPlugin(mf.Name)
}

// LoadPlugin applies a plugin's contributions: its widget packages, and — when
// it ships an executable — the process whose node types, connection kinds,
// port types, and field controls register through the same Host the built-ins
// use. Failures are recorded on the plugin's row so the panel can explain
// them.
func (m *Manager) LoadPlugin(name string) error {
	m.mu.Lock()
	mf, discovered := m.found[name]
	_, already := m.loaded[name]
	already = already || m.loading[name]
	if discovered && !already {
		m.loading[name] = true
	}
	m.mu.Unlock()

	if !discovered {
		return fmt.Errorf("plugin %q was not found on disk", name)
	}
	if already {
		return fmt.Errorf("plugin %q is already loaded", name)
	}
	defer func() {
		m.mu.Lock()
		delete(m.loading, name)
		m.mu.Unlock()
	}()

	lp, err := m.apply(mf)
	if err != nil {
		m.setStatus(name, models.PluginStatusError, err.Error())
		return err
	}

	m.mu.Lock()
	m.loaded[name] = lp
	m.mu.Unlock()
	m.setStatus(name, models.PluginStatusLoaded, "")

	m.log.Info("plugin loaded",
		zap.String("plugin", name),
		zap.Bool("process", mf.HasProcess()),
		zap.Int("widgets", len(mf.Widgets)))
	return nil
}

// apply performs the load, leaving nothing registered when it fails partway.
func (m *Manager) apply(mf ExternalManifest) (*loadedPlugin, error) {
	if len(mf.Widgets) > 0 {
		if err := widgets.Register(m.log, mf.Name, mf.Dir, mf.Widgets); err != nil {
			return nil, err
		}
		m.host.AttachWidgets(mf.Name, packageNames(mf.Widgets))
	}

	// A directory naming a compiled-in plugin holds that plugin's own files
	// rather than a second plugin: its widgets attach to the entry already in
	// the catalog, and there is nothing further to load.
	if m.host.IsBuiltin(mf.Name) {
		if mf.HasProcess() {
			widgets.Unregister(mf.Name)
			return nil, fmt.Errorf("plugin %q is built in and cannot supply an executable", mf.Name)
		}
		return &loadedPlugin{manifest: mf, builtin: true}, nil
	}

	if !mf.HasProcess() {
		if err := m.host.Load(widgetOnlyPlugin{mf: mf}); err != nil {
			widgets.Unregister(mf.Name)
			return nil, err
		}
		m.host.AttachWidgets(mf.Name, packageNames(mf.Widgets))
		return &loadedPlugin{manifest: mf}, nil
	}
	lp, err := m.loadProcess(mf)
	if err != nil {
		widgets.Unregister(mf.Name)
		return nil, err
	}
	m.host.AttachWidgets(mf.Name, packageNames(mf.Widgets))
	return lp, nil
}

// packageNames lists the widget packages a manifest declares.
func packageNames(specs []widgets.Spec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.PackageName)
	}
	return out
}

// UnloadPlugin reverses a plugin's contributions and stops its process.
func (m *Manager) UnloadPlugin(name string) error {
	m.mu.Lock()
	lp, ok := m.loaded[name]
	if ok && lp.builtin {
		m.mu.Unlock()
		return fmt.Errorf("plugin %q is built in and cannot be unloaded", name)
	}
	if ok {
		delete(m.loaded, name)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", name)
	}

	m.teardown(lp)
	m.setStatus(name, models.PluginStatusUnloaded, "")
	m.log.Info("plugin unloaded", zap.String("plugin", name))
	return nil
}

// teardown stops a plugin process and withdraws everything it registered.
func (m *Manager) teardown(lp *loadedPlugin) {
	name := lp.manifest.Name
	if lp.rpc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout())
		_, _ = lp.rpc.Shutdown(ctx, &pb.Empty{})
		cancel()
	}
	if lp.client != nil {
		lp.client.Kill()
	}
	if lp.host != nil {
		lp.host.releaseAll()
	}
	widgets.Unregister(name)
	if err := m.host.Unload(name); err != nil {
		m.log.Debug("nothing to withdraw from the catalog",
			zap.String("plugin", name), zap.Error(err))
	}
}

// ReloadPlugin unloads and loads a plugin, which is how a settings change
// takes effect: a plugin reads its configuration once, at Init.
func (m *Manager) ReloadPlugin(name string) error {
	if m.IsLoaded(name) {
		if err := m.UnloadPlugin(name); err != nil {
			return err
		}
	}
	return m.LoadPlugin(name)
}

// SetEnabled records whether a plugin loads at startup and brings its current
// state in line: disabling unloads it, enabling loads it.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	if m.host.IsBuiltin(name) {
		return fmt.Errorf("plugin %q is built in and is always enabled", name)
	}
	if db.DB != nil {
		state, err := models.GetPluginState(name)
		if err != nil {
			return fmt.Errorf("plugin %q not found", name)
		}
		state.Enabled = enabled
		if err := models.SavePluginState(state); err != nil {
			return err
		}
	}
	if enabled {
		if m.IsLoaded(name) {
			return nil
		}
		return m.LoadPlugin(name)
	}
	if !m.IsLoaded(name) {
		return nil
	}
	return m.UnloadPlugin(name)
}

// IsLoaded reports whether a plugin's contributions are applied.
func (m *Manager) IsLoaded(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.loaded[name]
	return ok
}

// Discovered returns every plugin found on disk, by name.
func (m *Manager) Discovered() map[string]ExternalManifest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]ExternalManifest, len(m.found))
	for name, mf := range m.found {
		out[name] = mf
	}
	return out
}

// SettingsSchema returns a loaded plugin's JSON Schema, empty when it declares
// no settings or is not loaded.
func (m *Manager) SettingsSchema(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lp, ok := m.loaded[name]; ok {
		return lp.settingsSchema
	}
	return ""
}

// HandleHTTP forwards a proxied request to a loaded plugin. It reports false
// when the plugin is not loaded or serves no API, which the caller turns into
// a 503.
func (m *Manager) HandleHTTP(ctx context.Context, name string, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, bool, error) {
	m.mu.Lock()
	lp, ok := m.loaded[name]
	m.mu.Unlock()
	if !ok || lp.rpc == nil {
		return nil, false, nil
	}

	resp, err := lp.rpc.HandleHTTP(ctx, &pb.HTTPRequest{
		Method:      req.Method,
		Path:        req.Path,
		Headers:     req.Headers,
		Body:        req.Body,
		QueryString: req.QueryString,
		RemoteAddr:  req.RemoteAddr,
		UserId:      req.UserID,
		Username:    req.Username,

		DashboardGrant: req.DashboardGrant,
		KeyLimited:     req.KeyLimited,
		KeyPermissions: req.KeyPermissions,
	})
	if err != nil {
		return nil, true, err
	}
	return &plugin.HTTPResponseResult{
		StatusCode: int(resp.StatusCode),
		Headers:    resp.Headers,
		Body:       resp.Body,
	}, true, nil
}

// Stop unloads every plugin and stops the liveness watcher.
func (m *Manager) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	m.wg.Wait()

	m.mu.Lock()
	loaded := m.loaded
	m.loaded = make(map[string]*loadedPlugin)
	m.mu.Unlock()

	for name, lp := range loaded {
		if lp.builtin {
			continue
		}
		m.teardown(lp)
		m.setStatus(name, models.PluginStatusUnloaded, "")
	}
}

// livenessLoop notices a plugin process that exited on its own, so the panel
// reports it as errored instead of quietly answering nothing.
func (m *Manager) livenessLoop() {
	defer m.wg.Done()

	interval := 30 * time.Second
	if config.C != nil && config.C.Plugins.HealthInterval > 0 {
		interval = time.Duration(config.C.Plugins.HealthInterval) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.checkLiveness()
		}
	}
}

func (m *Manager) checkLiveness() {
	m.mu.Lock()
	var dead []*loadedPlugin
	for name, lp := range m.loaded {
		if lp.client != nil && lp.client.Exited() {
			dead = append(dead, lp)
			delete(m.loaded, name)
		}
	}
	m.mu.Unlock()

	for _, lp := range dead {
		m.log.Warn("plugin process exited", zap.String("plugin", lp.manifest.Name))
		m.teardown(lp)
		m.setStatus(lp.manifest.Name, models.PluginStatusError, "process exited unexpectedly")
	}
}

// setStatus records a plugin's load result for the settings panel.
func (m *Manager) setStatus(name, status, errMsg string) {
	if db.DB == nil {
		return
	}
	state, err := models.GetPluginState(name)
	if err != nil {
		return
	}
	state.Status = status
	state.ErrorMsg = errMsg
	if err := models.SavePluginState(state); err != nil {
		m.log.Warn("failed to record plugin status", zap.String("plugin", name), zap.Error(err))
	}
}

// shutdownTimeout bounds a graceful plugin shutdown.
func shutdownTimeout() time.Duration {
	if config.C != nil && config.C.Plugins.ShutdownTimeout > 0 {
		return time.Duration(config.C.Plugins.ShutdownTimeout) * time.Second
	}
	return 10 * time.Second
}

// widgetOnlyPlugin records a plugin that ships no executable, so the catalog
// lists it alongside the ones that do.
type widgetOnlyPlugin struct{ mf ExternalManifest }

func (p widgetOnlyPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        p.mf.Name,
		Version:     p.mf.Version,
		Description: p.mf.Description,
		Author:      p.mf.Author,
	}
}

func (widgetOnlyPlugin) Register(plugin.Registrar) error { return nil }

// loadProcess starts a plugin process, serves it its own host callback
// channel, and registers what it contributes.
func (m *Manager) loadProcess(mf ExternalManifest) (*loadedPlugin, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  plugin.Handshake,
		Plugins:          map[string]goplugin.Plugin{plugin.PluginSetName: &plugin.GRPCPlugin{}},
		Cmd:              exec.Command(mf.Executable),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		GRPCDialOptions:  []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(plugin.MaxMessageBytes))},
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:        "plugin." + mf.Name,
			Level:       hclog.Info,
			Output:      zapLineWriter{log: m.log.Named("plugin." + mf.Name)},
			DisableTime: true,
		}),
	})

	ph := newPluginHost(m.svc, mf.Name, m.log)
	fail := func(err error) (*loadedPlugin, error) {
		client.Kill()
		ph.releaseAll()
		return nil, err
	}

	rpcClient, err := client.Client()
	if err != nil {
		return fail(fmt.Errorf("handshake failed: %w", err))
	}
	raw, err := rpcClient.Dispense(plugin.PluginSetName)
	if err != nil {
		return fail(fmt.Errorf("dispense failed: %w", err))
	}
	handle, ok := raw.(*plugin.ClientHandle)
	if !ok {
		return fail(fmt.Errorf("plugin dispensed an unexpected client type"))
	}

	// Serve the callback channel through the broker, then init the plugin
	// with the ID it dials back on. The channel is
	// per-plugin so every registration made through it carries the plugin's
	// name and can be withdrawn again.
	brokerID := handle.Broker.NextId()
	go handle.Broker.AcceptAndServe(brokerID, func(opts []grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(plugin.MaxMessageBytes))...)
		pb.RegisterKarakuriHostServer(s, ph)
		return s
	})

	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	initResp, err := handle.Client.Init(ctx, &pb.InitRequest{HostVersion: "1", HostServiceId: brokerID})
	if err != nil {
		return fail(fmt.Errorf("init failed: %w", err))
	}
	if !initResp.Success {
		return fail(fmt.Errorf("init failed: %s", initResp.Error))
	}
	regs, err := handle.Client.GetRegistrations(ctx, &pb.Empty{})
	if err != nil {
		return fail(fmt.Errorf("registration query failed: %w", err))
	}

	lp := &loadedPlugin{manifest: mf, client: client, rpc: handle.Client, host: ph}
	ext, err := newExternalPlugin(lp, m.svc, regs)
	if err != nil {
		return fail(err)
	}
	if err := m.host.Load(ext); err != nil {
		return fail(err)
	}

	// Messages can now be delivered, and the plugin's settings schema is
	// read once so defaults seed and the settings form has something to
	// render without another round trip.
	ph.attach(handle.Client)
	lp.settingsSchema = m.readSettingsSchema(ctx, mf.Name, handle.Client)
	m.seedSettingsDefaults(mf.Name, lp.settingsSchema)

	m.log.Debug("plugin process registered",
		zap.String("plugin", ext.manifest.Name),
		zap.Int("nodes", len(regs.Nodes)),
		zap.Int("kinds", len(regs.Kinds)))
	return lp, nil
}

// readSettingsSchema asks a plugin for its settings schema, treating an
// unimplemented RPC as "no settings" rather than a load failure.
func (m *Manager) readSettingsSchema(ctx context.Context, name string, rpc pb.KarakuriPluginClient) string {
	resp, err := rpc.GetSettingsSchema(ctx, &pb.Empty{})
	if err != nil {
		m.log.Debug("plugin declares no settings", zap.String("plugin", name), zap.Error(err))
		return ""
	}
	return resp.JsonSchema
}

// seedSettingsDefaults writes the default of every schema property that has no
// stored value, so a plugin's first load leaves a settings form filled in
// rather than blank.
func (m *Manager) seedSettingsDefaults(name, schema string) {
	if db.DB == nil || schema == "" {
		return
	}
	var parsed struct {
		Properties map[string]struct {
			Default any `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		m.log.Debug("unparsable settings schema", zap.String("plugin", name), zap.Error(err))
		return
	}

	for key, prop := range parsed.Properties {
		if prop.Default == nil {
			continue
		}
		fullKey := "plugins." + name + "." + key
		if _, err := models.GetSetting(fullKey); err == nil {
			continue
		}
		value, ok := prop.Default.(string)
		if !ok {
			enc, err := json.Marshal(prop.Default)
			if err != nil {
				continue
			}
			value = string(enc)
		}
		if err := models.SetSetting(fullKey, value); err != nil {
			m.log.Warn("failed to seed plugin setting",
				zap.String("key", fullKey), zap.Error(err))
		}
	}
}

// zapLineWriter routes go-plugin's log output (including forwarded plugin
// stderr) into the host's structured log.
type zapLineWriter struct{ log *zap.Logger }

func (w zapLineWriter) Write(p []byte) (int, error) {
	if line := strings.TrimSpace(string(p)); line != "" {
		w.log.Info(line)
	}
	return len(p), nil
}

// externalPlugin adapts one running plugin process to the plugin.Plugin interface,
// so Host.Load records and registers it exactly like a built-in.
type externalPlugin struct {
	lp       *loadedPlugin
	svc      *hostService
	manifest plugin.Manifest
	regs     *pb.Registrations
}

func newExternalPlugin(lp *loadedPlugin, svc *hostService, regs *pb.Registrations) (*externalPlugin, error) {
	var manifest plugin.Manifest
	if len(regs.ManifestJson) > 0 {
		if err := json.Unmarshal(regs.ManifestJson, &manifest); err != nil {
			return nil, fmt.Errorf("invalid plugin manifest: %w", err)
		}
	}
	// The directory manifest is authoritative for identity; the wire manifest
	// fills whatever it left out.
	if lp.manifest.Name != "" {
		manifest.Name = lp.manifest.Name
	}
	if lp.manifest.Version != "" {
		manifest.Version = lp.manifest.Version
	}
	if lp.manifest.Description != "" {
		manifest.Description = lp.manifest.Description
	}
	if lp.manifest.Author != "" {
		manifest.Author = lp.manifest.Author
	}
	manifest.Builtin = false
	return &externalPlugin{lp: lp, svc: svc, manifest: manifest, regs: regs}, nil
}

func (e *externalPlugin) Manifest() plugin.Manifest { return e.manifest }

func (e *externalPlugin) Register(r plugin.Registrar) error {
	for _, n := range e.regs.Nodes {
		var meta flow.NodeMeta
		if err := json.Unmarshal(n.MetaJson, &meta); err != nil {
			return fmt.Errorf("invalid node metadata: %w", err)
		}
		base := &remoteNode{lp: e.lp, svc: e.svc, meta: meta}
		if n.Trigger {
			r.RegisterNode(&remoteTrigger{remoteNode: base})
		} else {
			r.RegisterNode(base)
		}
	}
	for _, k := range e.regs.Kinds {
		kind, err := e.remoteKind(k)
		if err != nil {
			return err
		}
		r.RegisterConnectionKind(kind)
	}
	if len(e.regs.PortTypesJson) > 0 {
		var defs []flow.PortTypeDef
		if err := json.Unmarshal(e.regs.PortTypesJson, &defs); err != nil {
			return fmt.Errorf("invalid port types: %w", err)
		}
		for _, def := range defs {
			r.RegisterPortType(def)
		}
	}
	if len(e.regs.ControlsJson) > 0 {
		var defs []flow.FieldControlDef
		if err := json.Unmarshal(e.regs.ControlsJson, &defs); err != nil {
			return fmt.Errorf("invalid field controls: %w", err)
		}
		for _, def := range defs {
			r.RegisterFieldControl(def)
		}
	}
	if len(e.regs.AiProvidersJson) > 0 {
		var defs []plugin.AIProviderDef
		if err := json.Unmarshal(e.regs.AiProvidersJson, &defs); err != nil {
			return fmt.Errorf("invalid ai providers: %w", err)
		}
		for _, def := range defs {
			r.RegisterAIProvider(def)
		}
	}
	return nil
}

// remoteKind builds the host-side ConnectionKind for a plugin-owned driver
// kind: the factory yields a proxy driver, and the validate/test hooks are
// RPCs into the plugin.
func (e *externalPlugin) remoteKind(k *pb.KindRegistration) (integrations.ConnectionKind, error) {
	var spec []flow.FieldSpec
	if len(k.ConfigSpecJson) > 0 {
		if err := json.Unmarshal(k.ConfigSpecJson, &spec); err != nil {
			return integrations.ConnectionKind{}, fmt.Errorf("invalid kind config spec: %w", err)
		}
	}
	lp := e.lp
	kindName := k.Kind
	kind := integrations.ConnectionKind{
		Kind: kindName, Label: k.Label, ConfigSpec: spec, Secrets: k.Secrets,
		Factory: func(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
			return newRemoteDriver(lp, kindName, model, deps), nil
		},
	}
	if k.HasValidate {
		kind.Validate = func(cfg []byte) error {
			ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
			defer cancel()
			resp, err := lp.rpc.ValidateKindConfig(ctx, &pb.KindConfigRequest{Kind: kindName, ConfigJson: cfg})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return errors.New(resp.Error)
			}
			return nil
		}
	}
	if k.HasTest {
		// A plugin kind always answers through Probe, which carries what the
		// test found when the plugin reports it and nothing otherwise.
		kind.Probe = func(ctx context.Context, id uuid.UUID, cfg []byte) (string, error) {
			resp, err := lp.rpc.TestKindConnection(ctx, &pb.TestConnectionRequest{
				Kind: kindName, ConnId: id.String(), ConfigJson: cfg,
			})
			if err != nil {
				return "", err
			}
			if resp.Error != "" {
				return resp.Detail, errors.New(resp.Error)
			}
			return resp.Detail, nil
		}
		kind.Test = func(ctx context.Context, id uuid.UUID, cfg []byte) error {
			_, err := kind.Probe(ctx, id, cfg)
			return err
		}
	}
	return kind, nil
}

// remoteNode proxies one plugin node type: metadata answered locally, Execute
// crossing the wire with a live callback handle.
type remoteNode struct {
	lp   *loadedPlugin
	svc  *hostService
	meta flow.NodeMeta
}

func (n *remoteNode) Meta() engine.NodeMeta { return n.meta }

// rawConfig re-renders the merged config the context decodes, so the plugin
// side reads exactly what an in-process node would.
func rawConfig(ec engine.ExecContext) ([]byte, error) {
	var cfg map[string]any
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return json.Marshal(cfg)
}

func (n *remoteNode) request(ec engine.ExecContext, ctxID string) (*pb.ExecuteRequest, error) {
	cfg, err := rawConfig(ec)
	if err != nil {
		return nil, err
	}
	ports, err := json.Marshal(ec.Ports())
	if err != nil {
		return nil, err
	}
	// Wiring is stamped per output port so the plugin-side context answers
	// Wired without a callback round trip; it cannot change mid-execution.
	var wired []string
	for _, out := range ec.Ports().Outputs {
		if ec.Wired(out.ID) {
			wired = append(wired, out.ID)
		}
	}
	return &pb.ExecuteRequest{
		CtxId: ctxID, NodeType: n.meta.Type, NodeId: ec.NodeID(),
		ConfigJson: cfg, PortsJson: ports, WiredOutputs: wired,
	}, nil
}

func (n *remoteNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	// An agent's node runs only for a run whose user may use the agent; every
	// path into a node, script node calls included, lands here.
	if err := access.CheckRunAgentType(ctx, n.meta.Type); err != nil {
		return nil, err
	}
	ctxID := n.svc.register(ec)
	defer n.svc.release(ctxID)

	req, err := n.request(ec, ctxID)
	if err != nil {
		return nil, err
	}
	req.Inputs = make(map[string]*pb.WireMsg, len(inputs))
	for port, m := range inputs {
		w, err := plugin.MsgToWire(m)
		if err != nil {
			return nil, err
		}
		req.Inputs[port] = w
	}

	resp, err := n.lp.rpc.ExecuteNode(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", n.lp.manifest.Name, err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return plugin.OutputsFromWire(resp.Outputs)
}

// remoteTrigger arms a plugin trigger: emitted messages stream back until the
// arm context is canceled.
type remoteTrigger struct{ *remoteNode }

func (t *remoteTrigger) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	if err := access.CheckRunAgentType(ctx, t.meta.Type); err != nil {
		return err
	}
	ctxID := t.svc.register(ec)
	defer t.svc.release(ctxID)

	req, err := t.request(ec, ctxID)
	if err != nil {
		return err
	}
	stream, err := t.lp.rpc.StartTrigger(ctx, &pb.StartTriggerRequest{
		CtxId: req.CtxId, NodeType: req.NodeType, NodeId: req.NodeId,
		ConfigJson: req.ConfigJson, PortsJson: req.PortsJson,
		WiredOutputs: req.WiredOutputs,
	})
	if err != nil {
		return fmt.Errorf("plugin %s: %w", t.lp.manifest.Name, err)
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		msg, err := plugin.MsgFromWire(ev.Msg)
		if err != nil || msg == nil {
			continue
		}
		emit(msg)
	}
}

// remoteDriver proxies a connection driver living in the plugin process:
// Start opens the status stream, transitions relay to the connection manager,
// and Stop tears the remote driver down.
type remoteDriver struct {
	lp    *loadedPlugin
	kind  string
	model integrations.ConnModel
	deps  integrations.Deps

	mu     sync.Mutex
	status integrations.Status
	cancel context.CancelFunc
}

func newRemoteDriver(lp *loadedPlugin, kind string, model integrations.ConnModel, deps integrations.Deps) *remoteDriver {
	return &remoteDriver{
		lp: lp, kind: kind, model: model, deps: deps,
		status: integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}
}

func (d *remoteDriver) ID() uuid.UUID { return d.model.ID }
func (d *remoteDriver) Kind() string  { return d.kind }

func (d *remoteDriver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

func (d *remoteDriver) Start(ctx context.Context) error {
	sctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	stream, err := d.lp.rpc.StartDriver(sctx, &pb.StartDriverRequest{
		Kind: d.kind, ConnId: d.model.ID.String(), Name: d.model.Name, ConfigJson: d.model.Config,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("plugin %s: %w", d.lp.manifest.Name, err)
	}
	first, err := stream.Recv()
	if err != nil {
		cancel()
		return fmt.Errorf("plugin %s: %w", d.lp.manifest.Name, err)
	}
	if first.StartError != "" {
		cancel()
		return errors.New(first.StartError)
	}

	go func() {
		for {
			ev, err := stream.Recv()
			if err != nil {
				// Stop cancels the stream, which is the driver ending as
				// asked. Any other end is the plugin going away with the
				// driver in it, and the last status it sent no longer holds.
				if sctx.Err() == nil {
					d.setStatus(integrations.Status{
						State: integrations.StateError,
						Error: fmt.Sprintf("plugin %s stopped: %v", d.lp.manifest.Name, err),
						Since: time.Now(),
					})
				}
				return
			}
			d.setStatus(statusFromWire(ev.Status))
		}
	}()
	return nil
}

// setStatus records a status and relays it to the connection manager.
func (d *remoteDriver) setStatus(st integrations.Status) {
	d.mu.Lock()
	d.status = st
	d.mu.Unlock()
	if d.deps.PublishStatus != nil {
		d.deps.PublishStatus(st)
	}
}

func (d *remoteDriver) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = d.lp.rpc.StopDriver(ctx, &pb.DriverID{ConnId: d.model.ID.String()})
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
}

// statusFromWire decodes one driver status transition.
func statusFromWire(w *pb.DriverStatus) integrations.Status {
	if w == nil {
		return integrations.Status{}
	}
	since, _ := time.Parse(time.RFC3339Nano, w.Since)
	return integrations.Status{State: w.State, Error: w.Error, Since: since}
}
