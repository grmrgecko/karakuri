package main

import (
	"path/filepath"

	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/internal/update"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/historyfwd"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/grmrgecko/karakuri/pkg/tswriter"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/logger"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/api"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/autoimport"
	"github.com/grmrgecko/karakuri/pkg/backup"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/eventsources"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/homeops"
	khttp "github.com/grmrgecko/karakuri/pkg/http"
	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/mcpserver"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/nodes/energynodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/esphomenodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/mqttnodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/nvrnodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/openhabnodes"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"github.com/grmrgecko/karakuri/pkg/render"
	"github.com/grmrgecko/karakuri/pkg/themes"
	"github.com/grmrgecko/karakuri/pkg/zigbee2mqtt"
	"go.uber.org/zap"
)

// ServeCmd starts the HTTP server with all subsystems initialized.
type ServeCmd struct{}

func (c *ServeCmd) Run() error {
	// Create the logger from configuration.
	log, err := logger.New(config.C.Logging.Level, config.C.Logging.Format)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer func() { _ = log.Sync() }()

	// The generated config ships shared placeholder secrets so rebuilds cannot
	// orphan stored data. They are public, so say so loudly until replaced.
	if stale := config.C.UsesDefaultSecrets(); len(stale) > 0 {
		log.Warn("configuration still uses the shared placeholder secrets; "+
			"replace them before exposing this instance. Run `karakuri config rotate-key` "+
			"to generate an encryption key and migrate stored secrets to it",
			zap.Strings("settings", stale))
	}

	// A version installed by an automatic update gets a few starts to come
	// up healthy. Past them its predecessor's files are back in place, and
	// exiting lets the service manager start that version instead.
	pending, err := update.Recover(resources.GetDataPath(), version.Version)
	if err != nil {
		log.Warn("could not read the pending update record", zap.Error(err))
	}
	if pending.RolledBack {
		log.Error("the updated version failed to start; restored the previous version",
			zap.String("failed", pending.To), zap.String("restored", pending.From))
		os.Exit(updateRestartCode)
	}

	// Launched by a service manager rather than a terminal: hand the process
	// to the service wrapper so a stop request unwinds through the same
	// shutdown path as a signal.
	// The scope only decides where a unit is written, so a process already
	// started by a manager describes itself as the system's and lets Attach
	// answer whichever one launched it.
	if err := service.Attach(serviceDefinition(false, ""), signalStop); err != nil {
		return err
	}

	// Claim the data directory before opening anything in it. A second server
	// on the same directory would interleave writes to the spool and the
	// flow databases, and a restore must be able to tell a server is running.
	releaseLock, err := instlock.Acquire(resources.GetDataPath())
	if err != nil {
		return err
	}
	defer releaseLock()

	// Open the database, run migrations, and seed defaults.
	startLog = log
	if err := initDatabase(); err != nil {
		return err
	}

	// Register default permissions.
	permissions.RegisterDefaults()

	// Start the event log before anything that reports into it.
	if _, err := events.LoadSettings(); err != nil {
		log.Error("failed to load event log settings", zap.Error(err))
	}
	events.R = events.NewRecorder(mq.B, log.Named("events"))
	events.R.Start()
	stopSweeper := make(chan struct{})
	go events.RunSweeper(stopSweeper, log.Named("events"))
	events.Record(events.Entry{Category: models.EventSystem, Subject: events.Subject{Kind: "server", Name: "karakuri"},
		Message: "Server started (version " + version.Version + ")"})
	recordRestore(log)

	// Register dashboard theme packages found on disk. A bad package is logged
	// and skipped so one broken manifest cannot prevent startup. Widgets are a
	// plugin contribution and register with their plugin, below.
	if err := themes.LoadAndRegister(log); err != nil {
		log.Warn("failed to load themes", zap.Error(err))
	}

	// Install the script sandbox before registering node types, so the script
	// node reports accurate per-language availability from the first request.
	initScriptSandbox(log)

	// Load the built-in plugins. Everything a node set contributes — node
	// types, connection kinds, port types, field controls — goes through the
	// plugin host, so an external plugin loads through the same door.
	pluginHost := pluginhost.NewHost(engine.Default())
	for _, p := range []plugin.Plugin{core.Plugin(), mqttnodes.Plugin(), hanodes.Plugin(), esphomenodes.Plugin(), openhabnodes.Plugin(), nvrnodes.Plugin(), energynodes.Plugin(), ainodes.Plugin()} {
		if err := pluginHost.Load(p); err != nil {
			return fmt.Errorf("failed to load built-in plugin: %w", err)
		}
	}
	api.SetPluginHost(pluginHost)

	// Initialize WebSocket hub and gate topics behind read access. This comes
	// before plugin loading: a plugin subscribes to topics and registers its
	// own topic gates while it initializes.
	mq.InitHub(log)
	// A flow's live runs and editor notices follow the flow's shares, so a
	// socket hears only flows its user may read.
	mq.WS.RegisterTopicPermissionCallback("flow:*", func(user, topic string) bool {
		id, _, _ := strings.Cut(strings.TrimPrefix(topic, "flow:"), ":")
		return flowops.CanWatchFlow(user, id, permissions.PermRunsRead)
	})
	mq.WS.RegisterTopicPermissionCallback("editor:flow:*", func(user, topic string) bool {
		id, _, _ := strings.Cut(strings.TrimPrefix(topic, "editor:flow:"), ":")
		return flowops.CanWatchFlow(user, id, permissions.PermFlowsRead)
	})
	mq.WS.RegisterTopicPermissionCallback("editor:dashboard:*", func(user, topic string) bool {
		uid, err := uuid.Parse(user)
		if err != nil {
			return false
		}
		// Sockets never authenticate with an API key, so no key limit applies.
		bg := context.Background()
		d, err := dashops.ResolveFor(bg, uid, strings.TrimPrefix(topic, "editor:dashboard:"))
		return err == nil && dashops.CanEdit(bg, uid, d)
	})
	// A dashboard's runtime display state is part of viewing it, so the gate
	// is the view check itself: public dashboards answer anonymous sockets.
	mq.WS.RegisterTopicPermissionCallback("dashboard:*", func(user, topic string) bool {
		ref, ok := strings.CutSuffix(strings.TrimPrefix(topic, "dashboard:"), ":live")
		if !ok {
			return false
		}
		var uid uuid.UUID
		if user != "" {
			parsed, err := uuid.Parse(user)
			if err != nil {
				return false
			}
			uid = parsed
		}
		bg := context.Background()
		d, err := dashops.ResolveFor(bg, uid, ref)
		return err == nil && dashops.CanView(bg, uid, d)
	})
	// The run stream and list notices behind the flow and dashboard lists.
	// Runs are filtered per message by the flow's shares. List notices carry
	// no data; a page reads the list again through the API, which applies the
	// reader's shares.
	mq.WS.RegisterTopicPermissionCallback(engine.RunsTopic, func(user, _ string) bool {
		return userHas(user, permissions.PermRunsRead)
	})
	mq.WS.RegisterMessageFilter(engine.RunsTopic, func(user string, msg *mq.Message) bool {
		var ev struct {
			Run struct {
				FlowID string `json:"flow_id"`
			} `json:"run"`
		}
		if json.Unmarshal(msg.Payload, &ev) != nil {
			return false
		}
		return flowops.CanWatchFlow(user, ev.Run.FlowID, permissions.PermRunsRead)
	})
	mq.WS.RegisterTopicPermissionCallback(models.FlowsListTopic, func(user, _ string) bool {
		return userHas(user, permissions.PermFlowsRead)
	})
	// A dashboard can be shared with someone who holds no dashboard
	// permission, so any signed-in user may hear that the list changed.
	mq.WS.RegisterTopicPermissionCallback(models.DashboardsListTopic, func(user, _ string) bool {
		_, err := uuid.Parse(user)
		return err == nil
	})
	if err := models.WatchListChanges(func(topic string) {
		mq.B.Publish(topic, "changed", nil)
	}); err != nil {
		return fmt.Errorf("failed to watch list changes: %w", err)
	}
	mq.WS.RegisterTopicPermissionCallback("run:*", func(user, topic string) bool {
		id, _, _ := strings.Cut(strings.TrimPrefix(topic, "run:"), ":")
		return flowops.CanWatchRun(user, id, permissions.PermRunsRead)
	})
	// Connection topics name their connection after the first colon, so each
	// is gated on connection read and on using that connection.
	mq.WS.RegisterTopicPermissionCallback("connection:*", connectionTopicAllowed)
	mq.WS.RegisterTopicPermissionCallback("ha:*", connectionTopicAllowed)
	mq.WS.RegisterTopicPermissionCallback("mqtt:*", connectionTopicAllowed)
	// A device's decoded state is the same data as the topics behind it, so it
	// is behind the same gate, through the connection it speaks on.
	mq.WS.RegisterTopicPermissionCallback("mqttdevice:*", deviceTopicAllowed)
	// Controls gate per control rather than per user: one marked public is what
	// makes a public dashboard's rows live rather than frozen at page load.
	mq.WS.RegisterTopicPermissionCallback("control:*", controlTopicAllowed)
	// The event log's live topics follow its read permissions: security
	// entries need audit, and a subject's topic is also open to the dashboards
	// that show the subject, through the dashboard scope.
	mq.WS.RegisterTopicPermissionCallback("events", func(user, _ string) bool {
		return userHas(user, permissions.PermEventsRead)
	})
	mq.WS.RegisterTopicPermissionCallback("events:security", func(user, _ string) bool {
		return userHas(user, permissions.PermEventsAudit)
	})
	mq.WS.RegisterTopicPermissionCallback("events:subject:*", func(user, _ string) bool {
		return userHas(user, permissions.PermEventsRead)
	})
	// Flow entries follow the flow's shares, and connection, device, and agent
	// entries the resource's, so the live log drops them for a socket whose
	// user may not read the subject.
	liveFlowEvents := func(user string, msg *mq.Message) bool {
		var ev struct {
			SubjectKind string `json:"subject_kind"`
			SubjectID   string `json:"subject_id"`
		}
		if json.Unmarshal(msg.Payload, &ev) != nil {
			return false
		}
		uid, err := uuid.Parse(user)
		// A socket never authenticates with an API key, so no key limit applies.
		return err == nil && flowops.EventVisible(context.Background(), uid, ev.SubjectKind, ev.SubjectID)
	}
	mq.WS.RegisterMessageFilter("events", liveFlowEvents)
	mq.WS.RegisterMessageFilter("events:subject:*", liveFlowEvents)

	// The fallback for everything above: a client that holds no permission for
	// a topic but is displaying a dashboard whose deployed layout binds to it.
	// Without this a shared or public dashboard renders its first values over
	// HTTP and then never moves again.
	mq.WS.SetDashboardScope(dashscope.AllowsTopic)
	// And the same for a headless render's socket, which authenticated with a
	// render job's token instead of a session: it holds no account, so its
	// token is what answers the view check.
	mq.WS.SetRenderScope(dashscope.AllowsRenderTopic)
	mq.WS.SetRenderAuth(func(token string) (string, bool) {
		job, err := models.FindRenderJobByRenderToken(token)
		if err != nil {
			return "", false
		}
		return job.DashboardID.String(), job.Draft
	})

	// Dashboard notification endpoints raise alerts through the same command
	// path flows and editors use.
	notification.DashboardAlert = func(ref, title, message string, settings map[string]string) error {
		d, err := models.LookupDashboard(ref)
		if err != nil {
			return fmt.Errorf("dashboard %q not found", ref)
		}
		cmd := dashlive.Command{Action: dashlive.ActionAlert, Title: title, Message: message, Level: settings["level"], Style: settings["style"], Speak: settings["speak"] == "true"}
		if v := settings["duration"]; v != "" {
			seconds, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("dashboard alert duration %q is not a number", v)
			}
			cmd.Duration = &seconds
		}
		_, err = dashlive.S.Apply(d.ID, cmd)
		return err
	}

	// Load the controls into memory and announce their changes on the broker,
	// so a dashboard row and a flow see the same value the moment it moves.
	controls.S.SetPublisher(mq.B.Publish)
	if err := controls.S.Load(); err != nil {
		return fmt.Errorf("failed to load controls: %w", err)
	}

	// Load external plugins from the configured paths. A failing plugin is
	// logged and skipped so one bad package cannot prevent startup.
	// Calendars became connections; move feeds still held inline by calendar
	// nodes and widgets before the calendar plugin loads and reads them.
	migrate.MigrateCalendarFeeds(log)
	pluginMgr := pluginhost.NewManager(pluginHost, log)
	api.SetPluginManager(pluginMgr)
	if err := pluginMgr.LoadAll(pluginhost.SearchPaths(config.C.Plugins.Paths)); err != nil {
		return fmt.Errorf("failed to scan plugin paths: %w", err)
	}

	// Start the connection manager with all enabled connections. Home
	// Assistant drivers additionally relay entity state changes onto the
	// broker so dashboard widgets can render live device state; the topic is
	// gated behind connection read access below.
	connMgr := connmgr.New(log, mq.B)
	resolveConn := func(id uuid.UUID) (any, bool) { return connMgr.Get(id) }
	pluginhost.SetConnectionLookup(resolveConn)
	mqttBridge := mqttrelay.New(resolveConn, log)
	// Devices are the hand-written half of MQTT: declared topics and a decoder,
	// listening whether or not a dashboard is open, because a device that
	// reports only what changed cannot be caught up after the fact.
	mqttDevices := mqttrelay.NewDevices(resolveConn, log)
	// The device nodes read the same runner the API does, the way the control
	// nodes read controls.S.
	mqttrelay.D = mqttDevices
	// Devices restore the state they last reported, marked stale until they
	// report again, so a device that only reports changes is not blank after a
	// restart.
	mqttDevices.PersistState()
	// Availability follows each device's connection, and devices with an
	// expected reporting interval are swept for silence.
	mqttDevices.SetStatusWatcher(connMgr)
	stopAvailability := make(chan struct{})
	mqttDevices.StartAvailabilitySweep(stopAvailability)
	// Devices added from Zigbee2MQTT follow its device list: renames move
	// their topics, and devices it no longer lists are marked missing.
	z2mSync := zigbee2mqtt.NewSync(func(id uuid.UUID) (zigbee2mqtt.Subscriber, bool) {
		d, ok := connMgr.Get(id)
		if !ok {
			return nil, false
		}
		driver, ok := d.(*mqtt.Driver)
		return driver, ok
	}, log.Named("zigbee2mqtt"))
	mqttrelay.OnDevicesChanged(z2mSync.Refresh)
	entityTracker := entitydevices.NewTracker(log.Named("entities"))
	// Connections with auto import on add the devices they report and remove
	// the ones they stop reporting.
	autoimport.M = autoimport.New(log.Named("autoimport"))
	autoimport.M.OnRemove = tsdbops.DeleteOwned
	autoimport.M.Start(stopAvailability)
	startDriver := func(id uuid.UUID, d integrations.Driver) {
		// A replaced MQTT driver invalidates the relay's subscription handles,
		// so the topics a dashboard is already watching are re-established on
		// the new one.
		if node, ok := d.(*esphome.Driver); ok {
			// An ESPHome device's entity watch lives on the driver, and the
			// tracker follows the node's entity list for missing devices.
			mqttDevices.Rebind(id)
			mqttDevices.Reload()
			entityTracker.Attach(id, entitydevices.ESPHome(node))
			return
		}
		if recorder, ok := d.(*frigate.Driver); ok {
			// Event lists on dashboards refresh from the recorder's events.
			// The watch ends with the driver it was placed on.
			nvr.RelayEvents(id.String(), recorder, mq.B.Publish)
			// Frigate's camera switches are entity devices too, and a
			// camera leaving the config marks its switches missing.
			mqttDevices.Rebind(id)
			mqttDevices.Reload()
			entityTracker.Attach(id, entitydevices.Frigate(recorder))
			return
		}

		if _, ok := d.(*mqtt.Driver); ok {
			mqttBridge.Rebind(id)
			// A device's subscriptions live on the driver too. Rebind, not
			// Reload: the definitions did not change, so a reload would leave
			// every unchanged device holding the dead driver's handles.
			mqttDevices.Rebind(id)
			// And devices whose connection was down at boot start now.
			mqttDevices.Reload()
			z2mSync.Rebind(id)
			return
		}
		if oh, ok := d.(*openhab.Driver); ok {
			// openHAB items are entity devices; their watches live on the
			// driver, and the tracker follows the item list for missing ones.
			mqttDevices.Rebind(id)
			mqttDevices.Reload()
			entityTracker.Attach(id, entitydevices.OpenHAB(oh))
			return
		}
		ha, ok := d.(*homeassistant.Driver)
		if !ok {
			return
		}
		// Changes caught up after a reconnect go out on the same topics, so
		// dashboards, forwarding, and energy see them without asking.
		_, err := ha.SubscribeStateChangesDetailed(func(c homeassistant.StateChange) {
			mq.B.Publish(fmt.Sprintf("ha:%s:state:%s", id, c.EntityID), "ha_state", map[string]any{
				"entity_id": c.EntityID,
				"old_state": c.Old,
				"new_state": c.New,
				"caught_up": c.CaughtUp,
			})
		})
		if err != nil {
			log.Warn("failed to subscribe Home Assistant state changes",
				zap.String("connection_id", id.String()), zap.Error(err))
		}
		// Home Assistant entities can be devices too: their watches live on
		// the driver, and the tracker follows removals and renames.
		mqttDevices.Rebind(id)
		mqttDevices.Reload()
		entityTracker.Attach(id, entitydevices.HomeAssistant(ha))
	}
	connMgr.OnDriverStart = func(id uuid.UUID, d integrations.Driver) {
		startDriver(id, d)
		// Auto import attaches after the driver's devices are bound, so the
		// devices it adds start on the new driver.
		autoimport.M.Attach(id, d)
	}
	// The log's sources start before the connections, so their first
	// status reports are recorded.
	eventSources := eventsources.Start(mq.B, log.Named("events"))
	if err := connMgr.Start(); err != nil {
		return fmt.Errorf("failed to start connection manager: %w", err)
	}
	connops.SetManager(connMgr)
	homeops.SetLogger(log.Named("home"))

	// Load the memory stores and point their embedder at the AI connections.
	// The worker itself starts with the server context below.
	memories.S.SetEmbedFunc(func(ctx context.Context, connection, model string, texts []string) ([][]float32, error) {
		driver, err := connops.AIDriver(connection)
		if err != nil {
			return nil, err
		}
		return driver.Embed(ctx, model, texts)
	})
	if err := memories.S.Load(); err != nil {
		return fmt.Errorf("failed to load memory stores: %w", err)
	}
	if err := memories.S.Sweep(); err != nil {
		log.Warn("failed to sweep expired memories", zap.Error(err))
	}

	// Follow dashboard demand for MQTT topics. Nothing is subscribed upstream
	// until a widget watches a topic, and the subscription is dropped when the
	// last one leaves.
	mqttBridge.Start()
	mqttDevices.Reload()
	z2mSync.Refresh()

	// History writers deliver points to time-series connections, spooling
	// them on disk while a destination is down; buffers left by an earlier
	// run drain as soon as their connection answers. The forwarder feeds
	// them from controls, MQTT devices, and Home Assistant.
	tswriter.R = tswriter.NewRegistry(filepath.Join(resources.GetDataPath(), "history-spool"), tsdbops.ResolveSink, log.Named("history"))
	tswriter.R.Resume()
	connops.WriterStatus = func(id uuid.UUID) any {
		if st := tswriter.R.Status(id); st != nil {
			return st
		}
		return nil
	}
	connops.OnDelete(tswriter.R.Remove)
	connops.OnDelete(autoimport.M.Detach)
	historyfwd.F = historyfwd.New(historyfwd.Deps{
		Log:  log.Named("history"),
		Sink: tsdbops.ResolveSink,
		Writer: func(id uuid.UUID) (*tswriter.Writer, error) {
			return tswriter.R.For(id)
		},
		HAStates: func(ctx context.Context, id uuid.UUID) ([]homeassistant.Entity, error) {
			d, err := connops.HADriver(id.String())
			if err != nil {
				return nil, err
			}
			return d.StatesCached(ctx)
		},
		ConnectionName: func(id uuid.UUID) string {
			if c, err := models.FindConnectionByID(id); err == nil {
				return c.Name
			}
			return id.String()
		},
	})
	tsdbops.Reloader = historyfwd.F.Reload
	historyfwd.F.Start()
	// Energy usage is recorded from the same change streams.
	if err := energy.LoadSettings(); err != nil {
		log.Error("failed to load energy settings", zap.Error(err))
	}
	energy.QueryHistory = tsdbops.Query
	energy.R = energy.NewRecorder(log.Named("energy"))
	energy.R.Start()

	// Start the remote-agent listener when configured. A connected agent
	// registers its node types through the same plugin host the built-ins use,
	// so the engine sees it as one more plugin. Left disabled, the subsystem
	// costs nothing.
	var agentHub *agenthub.Hub
	if addr := config.C.Agents.Listen; addr != "" {
		hub, err := agenthub.New(pluginMgr, log, addr,
			time.Duration(config.C.Agents.EnrollTTL)*time.Second,
			time.Duration(config.C.Agents.CertTTL)*time.Second)
		if err != nil {
			return fmt.Errorf("failed to build agent hub: %w", err)
		}
		if err := pluginHost.Load(agenthub.Plugin(hub)); err != nil {
			return fmt.Errorf("failed to load agent lifecycle plugin: %w", err)
		}
		if err := hub.Start(context.Background()); err != nil {
			return fmt.Errorf("failed to start agent listener: %w", err)
		}
		agentHub = hub
		api.SetAgentHub(hub)
	}

	// Runs check the connections and agents their nodes reach against the
	// user each run acts as.
	access.EnableRunChecks()

	// Create and start the execution engine.
	eng := engine.New(engine.Options{
		Registry:          engine.Default(),
		Clock:             engine.RealClock{},
		Logger:            log,
		Publisher:         mq.B,
		History:           engine.NewDBHistory(log),
		VarSink:           engine.NewDBVarSink(log),
		NodeStateSink:     engine.NewDBNodeStateSink(log),
		WaitSink:          engine.NewDBWaitSink(log),
		MaxParallelNodes:  config.C.Engine.MaxParallelNodes,
		MaxRunsPerFlow:    config.C.Engine.MaxConcurrentRunsPerFlow,
		DefaultRunTimeout: time.Duration(config.C.Engine.DefaultRunTimeout) * time.Second,
		ConnResolver:      connMgr.Resolve,
	})

	// Seed the flow and global variable cache before any flow can run, so the
	// first message already sees what the last shutdown left behind.
	storedVars, err := models.ListVariables()
	if err != nil {
		return fmt.Errorf("failed to load variables: %w", err)
	}
	eng.LoadVariables(storedVars)

	// Seed the durable node stores the same way, so a dedupe history or a
	// filter baseline survives the restart it exists to survive.
	storedNodeState, err := models.ListNodeStoreEntries()
	if err != nil {
		return fmt.Errorf("failed to load node state: %w", err)
	}
	eng.LoadNodeState(storedNodeState)

	// Load pending waits before the runner arms flows: arming resumes them,
	// and it must find them already there.
	storedWaits, err := models.ListPendingWaits()
	if err != nil {
		return fmt.Errorf("failed to load pending waits: %w", err)
	}
	eng.LoadWaits(storedWaits)

	eng.EnableDBErrorFlows()
	eng.EnableDBSubflows()
	eng.Start()
	flowops.SetEngine(eng)
	engine.SetWebhookLogger(log)

	// Arm the triggers of deployed, enabled flows.
	runner := engine.NewRunner(eng, log)
	if err := runner.Start(); err != nil {
		return fmt.Errorf("failed to start trigger runner: %w", err)
	}
	flowops.SetTriggerRunner(runner)

	// Start the render service and its schedules. Nothing launches a browser
	// until something is rendered, so an install with no Chrome only finds out
	// when it asks for one.
	render.Init(log)
	if err := render.R.Start(); err != nil {
		return fmt.Errorf("failed to start render service: %w", err)
	}
	defer render.R.Stop()

	render.InitScheduler(log)
	if err := render.S.Start(); err != nil {
		return fmt.Errorf("failed to start render scheduler: %w", err)
	}
	defer render.S.Stop()

	// Build the MCP server once everything its tools call is wired: the
	// engine, the trigger runner, the connection manager, and the plugins.
	mcpserver.SetPlugins(pluginHost, pluginMgr)
	mcpserver.Init()

	// Start the HTTP server.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The memory embedder and expiry sweeper run for the server's lifetime.
	memories.S.Start(ctx, log)

	// Backups run on their schedule for the server's lifetime; a backup
	// still running at shutdown is cancelled and its partial file removed.
	backup.S = backup.NewService(backupSource, log.Named("backup"))
	eventSources.WatchBackups(backup.S)
	backup.Sched = backup.NewScheduler(backup.S, resources.GetDataPath(), config.C.Security.EncryptionKey, log.Named("backup"))
	go backup.Sched.Run(ctx)

	srv := khttp.NewHTTPServer(log)
	srv.Start(ctx)

	// The installed unit is Type=notify, so systemd holds dependent units
	// until this lands. It is a no-op when not running under systemd.
	if err := service.NotifyReady(); err != nil {
		log.Warn("failed to notify the service manager", zap.Error(err))
	}

	// Serving is the healthy start an automatic update waits for. The
	// version is recorded only now, so a release that never serves does not
	// claim the database, and the event log hears of the change however the
	// new release arrived.
	if pending.To == version.Version {
		if err := update.Confirm(resources.GetDataPath(), version.Version); err != nil {
			log.Warn("could not confirm the update", zap.Error(err))
		}
	}
	recordStart(log)

	// Check for updates daily when a service manager is there to start the
	// version an update installs.
	restart := make(chan struct{})
	switch {
	case config.C.Update.Enabled && service.Managed():
		go update.Loop(ctx, func(ctx context.Context) {
			if autoUpdate(ctx, log) {
				close(restart)
			}
		})
	case config.C.Update.Notify && migrate.IsRelease(version.Version):
		// A server that does not install releases, such as a container,
		// still reports a newer one.
		go update.Loop(ctx, func(ctx context.Context) { notifyUpdate(ctx, log) })
	}

	// Wait for a signal, the service manager asking for a stop, or an
	// installed update.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	restarting := false
	select {
	case <-done:
	case <-stopChan:
	case <-restart:
		restarting = true
	}
	log.Info("shutting down server")
	events.Record(events.Entry{Category: models.EventSystem, Subject: events.Subject{Kind: "server", Name: "karakuri"},
		Message: "Server stopping"})
	runner.Stop()
	if agentHub != nil {
		agentHub.Stop()
	}
	eng.Stop()
	mqttBridge.Stop()
	close(stopAvailability)
	z2mSync.Stop()
	entityTracker.Stop()
	mqttDevices.Stop()
	energy.R.Stop()
	eventSources.Stop()
	historyfwd.F.Stop()
	tswriter.R.Close()
	connMgr.Stop()
	pluginMgr.Stop()
	cancel()
	srv.Wait()
	close(stopSweeper)
	events.R.Stop()

	if restarting {
		log.Info("exiting for the service manager to start the updated version")
		_ = log.Sync()
		os.Exit(updateRestartCode)
	}
	return nil
}

// updateRestartCode is the exit status after an automatic update or a
// rollback. It is non-zero so every service manager restarts the process:
// systemd's Restart=always would on any status, but the Windows service
// manager and scheduler restart only a process that failed.
const updateRestartCode = 3

// controlTopicAllowed gates WebSocket subscription to a control's changes. A
// control marked public is readable by anyone, including an anonymous viewer
// of a public dashboard; everything else, and any wildcard subscription, needs
// a signed-in user holding control read access.
func controlTopicAllowed(userID, topic string) bool {
	if id, err := uuid.Parse(strings.TrimPrefix(topic, "control:")); err == nil {
		if snap, ok := controls.S.Get(id); ok && snap.AllowPublic {
			return true
		}
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	return auth.HasPermission(uid, permissions.PermControlsRead)
}

// recordRestore records a restore the command line made while the server was
// stopped, which it leaves as a setting, as the security log's first entry
// of the restored installation.
func recordRestore(log *zap.Logger) {
	row, err := models.GetSetting(backup.RestoredSetting)
	if err != nil {
		return
	}
	var r backup.Restored
	if err := json.Unmarshal([]byte(row.Value), &r); err == nil {
		message := "Restored backup " + r.Archive
		if r.Replaced {
			message += ", replacing the installation"
		}
		events.Record(events.Entry{
			Category: models.EventSecurity,
			Subject:  events.Subject{Kind: "backup", ID: r.Archive, Name: r.Archive},
			Message:  message,
			Details:  map[string]any{"version": r.Version, "taken": r.CreatedAt, "os_user": r.OSUser},
			Actor:    events.Actor{Kind: events.ActorSystem, Name: r.OSUser, Via: events.ViaCLI},
			At:       r.RestoredAt,
		})
	}
	if err := models.DeleteSetting(backup.RestoredSetting); err != nil {
		log.Error("clearing the restore marker", zap.Error(err))
	}
}

// userHas reports whether a WebSocket client's user holds a permission.
func userHas(user, permission string) bool {
	id, err := uuid.Parse(user)
	return err == nil && auth.HasPermission(id, permission)
}

// connectionTopicAllowed admits a connection:<id>, ha:<id>:..., or
// mqtt:<id>:... topic to a user with connection read who may use that
// connection.
func connectionTopicAllowed(user, topic string) bool {
	_, rest, _ := strings.Cut(topic, ":")
	connID, _, _ := strings.Cut(rest, ":")
	uid, err := uuid.Parse(user)
	if err != nil || !userHas(user, permissions.PermConnectionsRead) {
		return false
	}
	// A socket never authenticates with an API key, so no key limit applies.
	return access.CanUseConnection(context.Background(), uid, connID)
}

// deviceTopicAllowed admits an mqttdevice:<id> topic to a user with
// connection read who may use the connection the device speaks on.
func deviceTopicAllowed(user, topic string) bool {
	id, err := uuid.Parse(strings.TrimPrefix(topic, "mqttdevice:"))
	if err != nil {
		return false
	}
	device, err := models.FindMQTTDeviceByID(id)
	if err != nil {
		return false
	}
	return connectionTopicAllowed(user, "connection:"+device.ConnectionID.String())
}
