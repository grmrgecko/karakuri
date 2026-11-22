// Package homeops answers the overview's questions for one caller: what needs
// attention, what runs next, and what work is unfinished. REST and MCP share
// it, so the browser and assistants get the same answer under the same
// permission rules.
package homeops

import (
	"context"
	"errors"
	"fmt"
	"github.com/grmrgecko/karakuri/pkg/access"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/agentops"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/backup"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Attention sources, as AttentionResult names them.
const (
	SourceConnections = "connections"
	SourceAgents      = "agents"
	SourceFlows       = "flows"
	SourceBackups     = "backups"
	SourceHistory     = "history"
	SourceUpdates     = "updates"
)

// Attention severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

const (
	// connectingGrace is how long a connection may stay connecting before it
	// is reported; a shorter connect is ordinary startup.
	connectingGrace = 2 * time.Minute
	// flapWindow and flapDrops say when a connection that is up now is
	// unstable: it went down flapDrops or more times within flapWindow.
	flapWindow = time.Hour
	flapDrops  = 3
	// failureWindow is how far back flow failures are gathered.
	failureWindow = 24 * time.Hour
)

var log = zap.NewNop()

// SetLogger installs the logger for sources that fail to read.
func SetLogger(l *zap.Logger) {
	if l != nil {
		log = l
	}
}

// AttentionItem is one thing that needs a person to look at it.
type AttentionItem struct {
	Severity    string    `json:"severity" description:"error or warning."`
	Kind        string    `json:"kind" description:"connection, agent, flow, backup, history, or update."`
	SubjectID   string    `json:"subject_id" description:"ID of the connection, agent, or flow, or the backup archive."`
	SubjectName string    `json:"subject_name" description:"Name of the subject."`
	Message     string    `json:"message" description:"The error, or the condition that needs attention."`
	Count       int       `json:"count" description:"How many times it happened; 1 when once."`
	At          time.Time `json:"at" description:"When the condition began, or its newest occurrence."`
	Recovered   bool      `json:"recovered,omitempty" description:"For flow failures, whether the flow's newest finished run succeeded."`
	RunID       string    `json:"run_id,omitempty" description:"For flow failures, the newest failed run."`
	Link        string    `json:"link" description:"App path where the subject is inspected or fixed."`
}

// Summary carries the figures an all-clear line states. A field is absent
// when its source was not checked.
type Summary struct {
	ConnectionsConnected *int `json:"connections_connected,omitempty" description:"Enabled connections that are connected."`
	ConnectionsTotal     *int `json:"connections_total,omitempty" description:"Enabled connections."`
	AgentsOnline         *int `json:"agents_online,omitempty" description:"Enabled agents with a live session."`
	AgentsTotal          *int `json:"agents_total,omitempty" description:"Enabled agents."`
	FlowFailures         *int `json:"flow_failures,omitempty" description:"Failed runs in the last 24 hours."`
}

// AttentionResult is what needs attention, and which sources were looked at.
type AttentionResult struct {
	Items     []AttentionItem `json:"items" description:"Errors first, then newest first."`
	Checked   []string        `json:"checked" description:"Sources read."`
	Unchecked []string        `json:"unchecked" description:"Sources the caller may read that could not be read."`
	Summary   Summary         `json:"summary" description:"Figures for the all-clear line."`
}

// Attention gathers what needs attention from every source the caller may
// read. A source that fails is logged and named in Unchecked; the others
// still answer, so an empty list is all clear only for the sources checked.
func Attention(ctx context.Context, userID uuid.UUID, now time.Time) AttentionResult {
	res := AttentionResult{Items: []AttentionItem{}, Checked: []string{}, Unchecked: []string{}}
	// Connections and agents count only when the caller may use them, so
	// neither the items nor the summary figures reveal anyone else's.
	conns, connErr := access.ConnectionIDs(ctx, userID, access.Use)
	agents, agentErr := access.AgentIDs(ctx, userID, access.Use)
	sources := []struct {
		name       string
		permission string
		read       func(*AttentionResult) error
	}{
		{SourceConnections, perm.PermConnectionsRead, func(r *AttentionResult) error {
			if connErr != nil {
				return connErr
			}
			return connectionItems(r, now, conns)
		}},
		{SourceAgents, perm.PermAgentsRead, func(r *AttentionResult) error {
			if agentErr != nil {
				return agentErr
			}
			return agentItems(r, agents)
		}},
		{SourceFlows, perm.PermRunsRead, func(r *AttentionResult) error { return flowItems(ctx, userID, r, now) }},
		{SourceBackups, perm.PermBackupsRead, func(r *AttentionResult) error { return backupItems(r, now) }},
		{SourceHistory, perm.PermConnectionsRead, func(r *AttentionResult) error {
			if connErr != nil {
				return connErr
			}
			return historyItems(r, conns)
		}},
		// A newer release concerns whoever may install software on the
		// server, which plugin write stands for.
		{SourceUpdates, perm.PermPluginsWrite, updateItems},
	}
	for _, s := range sources {
		if !auth.Can(ctx, userID, s.permission) {
			continue
		}
		if err := s.read(&res); err != nil {
			log.Error("attention source could not be read", zap.String("source", s.name), zap.Error(err))
			res.Unchecked = append(res.Unchecked, s.name)
			continue
		}
		res.Checked = append(res.Checked, s.name)
	}
	sort.SliceStable(res.Items, func(i, j int) bool {
		a, b := res.Items[i], res.Items[j]
		if a.Severity != b.Severity {
			return a.Severity == SeverityError
		}
		return a.At.After(b.At)
	})
	return res
}

// connectionItems reports enabled connections in error, stuck connecting, or
// connected but dropping repeatedly.
func connectionItems(r *AttentionResult, now time.Time, allowed map[uuid.UUID]bool) error {
	mgr := connops.Manager()
	if mgr == nil {
		return fmt.Errorf("connection manager is not running")
	}
	conns, err := models.ListEnabledConnections()
	if err != nil {
		return err
	}
	drops, dropAt, err := connectionDrops(now.Add(-flapWindow))
	if err != nil {
		return err
	}
	connected := 0
	for _, c := range conns {
		if !access.Allowed(allowed, c.ID) {
			continue
		}
		st, ok := mgr.Status(c.ID)
		if !ok {
			continue
		}
		item := AttentionItem{Kind: "connection", SubjectID: c.ID.String(), SubjectName: c.Name, Count: 1, At: st.Since, Link: "/settings/connections"}
		switch st.State {
		case integrations.StateError:
			item.Severity = SeverityError
			item.Message = st.Error
			if item.Message == "" {
				item.Message = "connection failed"
			}
			r.Items = append(r.Items, item)
		case integrations.StateConnecting:
			if now.Sub(st.Since) > connectingGrace {
				item.Severity = SeverityWarning
				item.Message = "still connecting"
				r.Items = append(r.Items, item)
			}
		case integrations.StateConnected:
			connected++
			if n := drops[c.ID.String()]; n >= flapDrops {
				item.Severity = SeverityWarning
				item.Message = fmt.Sprintf("unstable: dropped %d times in the last hour", n)
				item.Count = n
				item.At = dropAt[c.ID.String()]
				r.Items = append(r.Items, item)
			}
		}
	}
	total := len(conns)
	r.Summary.ConnectionsConnected = &connected
	r.Summary.ConnectionsTotal = &total
	return nil
}

// connectionDrops counts each connection's failures since since, with the
// newest one's time, from the event log, which records every failed attempt
// and folds repeats into one row with a count.
func connectionDrops(since time.Time) (map[string]int, map[string]time.Time, error) {
	var rows []models.Event
	if err := db.DB.Select("subject_id", "count", "last_at").
		Where("category = ? AND level = ? AND subject_kind = ? AND last_at >= ?", models.EventConnection, "error", "connection", since).
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	newest := map[string]time.Time{}
	for _, e := range rows {
		counts[e.SubjectID] += e.Count
		if e.LastAt.After(newest[e.SubjectID]) {
			newest[e.SubjectID] = e.LastAt
		}
	}
	return counts, newest, nil
}

// agentItems reports enabled agents without a live session.
func agentItems(r *AttentionResult, allowed map[uuid.UUID]bool) error {
	if !agentops.Running() {
		return fmt.Errorf("agent hub is not running")
	}
	agents, err := models.ListAgents()
	if err != nil {
		return err
	}
	online, total := 0, 0
	for _, a := range agents {
		if !access.Allowed(allowed, a.ID) {
			continue
		}
		if !a.Enabled {
			continue
		}
		total++
		if agentops.Connected(a.ID) {
			online++
			continue
		}
		item := AttentionItem{Severity: SeverityError, Kind: "agent", SubjectID: a.ID.String(), SubjectName: a.Name,
			Message: "offline", Count: 1, Link: "/settings/agents"}
		if a.LastSeenAt != nil {
			item.At = *a.LastSeenAt
		} else {
			item.Message = "never connected"
			item.At = a.CreatedAt
		}
		r.Items = append(r.Items, item)
	}
	r.Summary.AgentsOnline = &online
	r.Summary.AgentsTotal = &total
	return nil
}

// flowItems groups failed runs of the last day by flow and error, for flows
// the caller may read. A run whose failure an error branch handled succeeded,
// so it is not a failure here.
func flowItems(ctx context.Context, userID uuid.UUID, r *AttentionResult, now time.Time) error {
	readable, err := flowops.ReadableFlowIDs(ctx, userID)
	if err != nil {
		return err
	}
	var runs []models.Run
	if err := db.DB.Select("id", "flow_id", "started_at", "error").
		Where("status = ? AND started_at >= ?", "failed", now.Add(-failureWindow)).
		Order("started_at DESC").Find(&runs).Error; err != nil {
		return err
	}

	type key struct {
		flow uuid.UUID
		err  string
	}
	groups := map[key]*AttentionItem{}
	var order []key
	flows := map[uuid.UUID]*models.Flow{}
	failures := 0
	for _, run := range runs {
		if readable != nil && !readable[run.FlowID] {
			continue
		}
		f, ok := flows[run.FlowID]
		if !ok {
			f, err = models.FindFlowByID(run.FlowID)
			if err != nil {
				continue
			}
			flows[run.FlowID] = f
		}
		failures++
		k := key{run.FlowID, run.Error}
		if g, ok := groups[k]; ok {
			g.Count++
			continue
		}
		msg := run.Error
		if msg == "" {
			msg = "run failed"
		}
		groups[k] = &AttentionItem{Severity: SeverityError, Kind: "flow", SubjectID: f.ID.String(), SubjectName: f.Name,
			Message: msg, Count: 1, At: run.StartedAt, RunID: run.ID.String(), Link: "/flows/" + f.ID.String()}
		order = append(order, k)
	}

	// A flow whose newest finished run succeeded has recovered; its failures
	// stay visible as warnings so an intermittent fault is not hidden.
	recovered := map[uuid.UUID]bool{}
	for id := range flows {
		var last models.Run
		err := db.DB.Select("status").Where("flow_id = ? AND status IN ?", id, []string{"succeeded", "failed"}).
			Order("started_at DESC").Limit(1).Find(&last).Error
		if err == nil && last.Status == "succeeded" {
			recovered[id] = true
		}
	}
	for _, k := range order {
		g := groups[k]
		if recovered[k.flow] {
			g.Severity = SeverityWarning
			g.Recovered = true
		}
		r.Items = append(r.Items, *g)
	}
	r.Summary.FlowFailures = &failures
	return nil
}

// backupItems reports a failed newest backup and a schedule that missed its
// time.
func backupItems(r *AttentionResult, now time.Time) error {
	if backup.S == nil {
		return fmt.Errorf("backup service is not running")
	}
	runs, err := models.ListBackupRuns(5)
	if err != nil {
		return err
	}
	// Only the newest finished run counts: a failure followed by a success
	// is resolved.
	for _, run := range runs {
		if run.Status == models.BackupRunning {
			continue
		}
		item := AttentionItem{Severity: SeverityError, Kind: "backup", SubjectID: run.Archive, SubjectName: "Backup",
			Count: 1, At: run.StartedAt, Link: "/settings/backup"}
		if run.FinishedAt != nil {
			item.At = *run.FinishedAt
		}
		if run.Status == models.BackupFailed {
			item.Message = "backup failed"
			if run.Error != "" {
				item.Message = "backup failed: " + run.Error
			}
			r.Items = append(r.Items, item)
		} else if run.Verified == models.BackupVerifyFailed {
			item.Message = "backup archive failed verification"
			r.Items = append(r.Items, item)
		}
		break
	}
	due, overdue, err := backup.S.Overdue(now)
	if err != nil {
		return err
	}
	if overdue {
		r.Items = append(r.Items, AttentionItem{Severity: SeverityWarning, Kind: "backup", SubjectName: "Scheduled backup",
			Message: "overdue: no successful backup since it was due", Count: 1, At: due, Link: "/settings/backup"})
	}
	return nil
}

// historyItems reports history writers whose newest delivery failed.
func historyItems(r *AttentionResult, allowed map[uuid.UUID]bool) error {
	statuses, err := tsdbops.Statuses()
	if err != nil {
		return err
	}
	for _, st := range statuses {
		if !access.Allowed(allowed, st.ConnectionID) {
			continue
		}
		if st.Writer == nil || st.Writer.State != "failing" {
			continue
		}
		item := AttentionItem{Severity: SeverityWarning, Kind: "history", SubjectID: st.ConnectionID.String(), SubjectName: st.Name,
			Message: "history is not being delivered", Count: 1, Link: "/settings/history"}
		if st.Writer.LastError != "" {
			item.Message = "history is not being delivered: " + st.Writer.LastError
		}
		if st.Writer.LastErrorAt != nil {
			item.At = *st.Writer.LastErrorAt
		}
		r.Items = append(r.Items, item)
	}
	return nil
}

// updateItems reports a newer release a check found, while it is newer than
// the running one. A server that installs releases itself never records one
// it could install, so this is the container's and the unwritable install's
// reminder.
func updateItems(r *AttentionResult) error {
	s, err := models.GetSetting(migrate.AvailableSetting)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cmp, ok := migrate.CompareVersions(s.Value, version.Version); !ok || cmp <= 0 {
		return nil
	}
	r.Items = append(r.Items, AttentionItem{Severity: SeverityWarning, Kind: "update", SubjectID: s.Value,
		SubjectName: "Karakuri " + s.Value, Message: "a newer release is available; this server runs " + version.Version,
		Count: 1, At: s.UpdatedAt, Link: "/help/configuration#automatic-updates"})
	return nil
}
