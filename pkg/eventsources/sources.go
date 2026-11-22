// Package eventsources feeds the event log from the parts of karakuri that
// already announce what happens: control and device watchers and broker
// topics. Changes reached through a service call are recorded where they
// happen instead.
package eventsources

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/backup"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"go.uber.org/zap"
)

// Sources holds the subscriptions feeding the log.
type Sources struct {
	broker *mq.Broker
	log    *zap.Logger

	mu    sync.Mutex
	stops []func()
	// haSubs are the broker subscriptions for listed Home Assistant
	// entities, replaced when the settings change.
	haSubs []string
	// lastState is each connection's last reported state and error, so a
	// repeated report is not a new event.
	lastState map[string]string
	// failed marks connections that failed since they last connected, so a
	// recovery reads as reconnected and a first connection does not.
	failed map[string]bool
}

// Start subscribes to every source. broker may be nil in tests.
func Start(broker *mq.Broker, log *zap.Logger) *Sources {
	if log == nil {
		log = zap.NewNop()
	}
	s := &Sources{broker: broker, log: log}
	if controls.S != nil {
		s.stops = append(s.stops, controls.S.Watch(controlChanged))
	}
	if mqttrelay.D != nil {
		s.stops = append(s.stops, mqttrelay.D.WatchAvailability(availabilityChanged))
	}
	if broker != nil {
		s.watchHA(events.Current())
		events.OnSettingsChange(s.watchHA)
		s.stops = append(s.stops, s.subscribe("connection:*:status", s.connectionStatus))
		s.stops = append(s.stops, s.subscribe("flow:*:runs", runFinished))
	}
	return s
}

// WatchBackups records backup results; serve calls it once the backup service
// exists, which is after the sources start.
func (s *Sources) WatchBackups(svc *backup.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops = append(s.stops, svc.Watch(backupFinished))
}

// subscribe returns a function ending a broker subscription.
func (s *Sources) subscribe(pattern string, fn mq.Handler) func() {
	id := s.broker.Subscribe(pattern, fn)
	return func() { s.broker.Unsubscribe(id) }
}

// connectionStatus records connections failing, recovering, and being
// turned off. Each failed attempt is recorded, so a connection retrying folds
// into one entry with a count; the connecting state between attempts is not
// an event, or the entries would alternate and never fold.
func (s *Sources) connectionStatus(msg *mq.Message) {
	var p struct {
		ConnectionID string `json:"connection_id"`
		State        string `json:"state"`
		Error        string `json:"error"`
	}
	if json.Unmarshal(msg.Payload, &p) != nil || p.ConnectionID == "" {
		return
	}
	s.mu.Lock()
	if s.lastState == nil {
		s.lastState, s.failed = map[string]string{}, map[string]bool{}
	}
	previous := s.lastState[p.ConnectionID]
	s.lastState[p.ConnectionID] = p.State + "\x00" + p.Error
	recovered := s.failed[p.ConnectionID] && p.State == "connected"
	switch p.State {
	case "error":
		s.failed[p.ConnectionID] = true
	case "connected", "disabled":
		delete(s.failed, p.ConnectionID)
	}
	s.mu.Unlock()
	if previous == p.State+"\x00"+p.Error {
		return
	}
	prevState, _, _ := strings.Cut(previous, "\x00")

	name := p.ConnectionID
	var conn models.Connection
	if db.DB.Select("name").Where("id = ?", p.ConnectionID).First(&conn).Error == nil {
		name = conn.Name
	}
	var level, message string
	switch p.State {
	case "error":
		level, message = models.EventError, name+" failed"
		if p.Error != "" {
			message += ": " + p.Error
		}
	case "connected":
		level, message = models.EventInfo, name+" connected"
		if recovered {
			message = name + " reconnected"
		}
	case "disabled":
		if prevState == "" {
			return
		}
		level, message = models.EventInfo, name+" stopped"
	default:
		return
	}
	events.Record(events.Entry{
		Category: models.EventConnection, Level: level,
		Subject: events.Subject{Kind: "connection", ID: p.ConnectionID, Name: name},
		Message: message,
		Details: map[string]any{"state": p.State, "error": p.Error},
	})
}

// runFinished records a failed run, linked to the run.
func runFinished(msg *mq.Message) {
	if msg.Type != "run_finished" {
		return
	}
	var p struct {
		Run struct {
			ID     string `json:"id"`
			FlowID string `json:"flow_id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"run"`
	}
	if json.Unmarshal(msg.Payload, &p) != nil || p.Run.Status != "failed" {
		return
	}
	name := p.Run.FlowID
	var f models.Flow
	if db.DB.Select("name").Where("id = ?", p.Run.FlowID).First(&f).Error == nil {
		name = f.Name
	}
	message := name + " failed"
	if p.Run.Error != "" {
		message += ": " + p.Run.Error
	}
	events.Record(events.Entry{
		Category: models.EventFlow, Level: models.EventError,
		Subject: events.Subject{Kind: "flow", ID: p.Run.FlowID, Name: name},
		Message: message,
		Details: map[string]any{"run_id": p.Run.ID, "error": p.Run.Error},
		Actor:   events.Actor{Kind: events.ActorFlow, ID: p.Run.FlowID, Name: name, RunID: p.Run.ID, Via: events.ViaFlow},
	})
}

// backupFinished records each backup's result.
func backupFinished(run models.BackupRun) {
	level, message := models.EventInfo, fmt.Sprintf("Backup %s finished (%s)", run.Archive, sizeText(run.Size))
	if run.Status != models.BackupSucceeded {
		level, message = models.EventError, "Backup failed: "+run.Error
	}
	events.Record(events.Entry{
		Category: models.EventSystem, Level: level,
		Subject: events.Subject{Kind: "backup", ID: run.Archive, Name: run.Archive},
		Message: message,
		Details: map[string]any{"trigger": run.Trigger, "run_id": run.ID, "size": run.Size},
	})
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Stop ends every subscription.
func (s *Sources) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stop := range s.stops {
		stop()
	}
	s.stops = nil
	if s.broker != nil {
		for _, id := range s.haSubs {
			s.broker.Unsubscribe(id)
		}
	}
	s.haSubs = nil
}

// controlChanged records a control's new value with whoever wrote it. A
// definition edit that leaves the value alone is not an event. A press of a
// buttons control is one even when the same button was pressed last, since
// its value records the press rather than a state.
func controlChanged(c controls.Change) {
	if events.Excluded(c.Control.ID.String()) {
		return
	}
	pressed := controls.PressOnly(c.Control.Kind) && !c.Definition
	if !pressed && reflect.DeepEqual(c.Old, c.Control.Value) {
		return
	}
	oldV, newV := c.Old, c.Control.Value
	msg := fmt.Sprintf("%s set to %s", c.Control.Name, events.FormatValue(newV))
	if c.Attribute != "" {
		oldV, newV = attr(c.Old, c.Attribute), attr(c.Control.Value, c.Attribute)
		msg = fmt.Sprintf("%s %s set to %s", c.Control.Name, c.Attribute, events.FormatValue(newV))
	}
	details := map[string]any{"old": oldV, "new": newV}
	if c.Attribute != "" {
		details["attribute"] = c.Attribute
	}
	events.Record(events.Entry{
		Category: models.EventDevice,
		Subject:  events.Subject{Kind: "control", ID: c.Control.ID.String(), Name: c.Control.Name},
		Message:  msg,
		Details:  details,
		Actor:    c.Actor,
		At:       c.At,
	})
}

func attr(v any, name string) any {
	if m, ok := v.(map[string]any); ok {
		return m[name]
	}
	return v
}

// availabilityChanged records a device going offline and coming back.
func availabilityChanged(c mqttrelay.AvailabilityChange) {
	level, msg := models.EventInfo, c.Name+" is "+c.Current
	switch c.Current {
	case "offline":
		level, msg = models.EventWarning, c.Name+" went offline"
	case "online":
		msg = c.Name + " is back online"
		if c.Previous == "" || c.Previous == "unknown" {
			msg = c.Name + " is online"
		}
	}
	events.Record(events.Entry{
		Category: models.EventDevice, Level: level,
		Subject: events.Subject{Kind: "device", ID: c.DeviceID.String(), Name: c.Name},
		Message: msg,
		Details: map[string]any{"previous": c.Previous, "current": c.Current, "source": c.Source},
		At:      c.At,
	})
}

// haState is the part of a Home Assistant state the log reads.
type haState struct {
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// watchHA subscribes to exactly the listed entities' state topics, replacing
// the previous subscriptions.
func (s *Sources) watchHA(settings events.Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.haSubs {
		s.broker.Unsubscribe(id)
	}
	s.haSubs = nil
	for _, e := range settings.HAEntities {
		conn, entity := e.ConnectionID, e.EntityID
		id := s.broker.Subscribe(fmt.Sprintf("ha:%s:state:%s", conn, entity), func(msg *mq.Message) {
			var p struct {
				Old *haState `json:"old_state"`
				New *haState `json:"new_state"`
			}
			if json.Unmarshal(msg.Payload, &p) != nil || p.New == nil {
				return
			}
			if p.Old != nil && p.Old.State == p.New.State {
				return
			}
			name := entity
			if fn, ok := p.New.Attributes["friendly_name"].(string); ok && fn != "" {
				name = fn
			}
			details := map[string]any{"new": p.New.State}
			if p.Old != nil {
				details["old"] = p.Old.State
			}
			events.Record(events.Entry{
				Category: models.EventDevice,
				Subject:  events.Subject{Kind: "ha_entity", ID: conn + "/" + entity, Name: name},
				Message:  name + " changed to " + p.New.State,
				Details:  details,
			})
		})
		s.haSubs = append(s.haSubs, id)
	}
}
