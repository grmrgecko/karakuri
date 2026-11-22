package migrate

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Calendar node types and connection kinds the migration knows. They belong
// to the calendar plugin, which has not loaded when migrations run, so the
// names are spelled out here.
const (
	calendarEventNode  = "calendar-event"
	calendarEventsNode = "calendar-events"
	calendarICSKind    = "calendar-ics"
	calendarGoogleKind = "calendar-google"
	calendarSourcesKey = "plugins.calendar.%.sources"
)

// settingKeyColumn names the settings key column through the dialect, which
// quotes it: MySQL reserves the word.
var settingKeyColumn = clause.Column{Name: "key"}

// calendarFeed is one inline feed: what a node or widget source held before
// calendars became connections.
type calendarFeed struct {
	provider, url, apiKey string
	// label names the connection created for the feed.
	label string
}

// key groups feeds that can share a connection.
func (f calendarFeed) key() string { return f.provider + "\x00" + f.url + "\x00" + f.apiKey }

// MigrateCalendarFeeds moves inline calendar feeds into calendar connections.
// Flow nodes in every version and draft, and calendar widget sources, that
// still hold a feed URL and API key are rewritten to name a connection created
// for the feed; feeds used in several places share one. It is idempotent: an
// item that already names a connection is left alone. An item that cannot be
// moved keeps its inline settings and is logged.
func MigrateCalendarFeeds(log *zap.Logger) {
	if log == nil {
		log = zap.NewNop()
	}
	var versions []models.FlowVersion
	if err := db.DB.Where("graph LIKE ?", "%calendar-event%").Find(&versions).Error; err != nil {
		log.Error("calendar migration: cannot read flow versions", zap.Error(err))
		return
	}
	var settings []models.Setting
	if err := db.DB.Where(clause.Like{Column: settingKeyColumn, Value: calendarSourcesKey}).Find(&settings).Error; err != nil {
		log.Error("calendar migration: cannot read widget sources", zap.Error(err))
		return
	}

	// Collect every inline feed first, so a feed used in several places gets
	// one connection.
	feeds := map[string]calendarFeed{}
	for _, v := range versions {
		var g map[string]any
		if json.Unmarshal([]byte(v.Graph), &g) != nil {
			continue
		}
		walkCalendarNodes(g, func(_ string, cfg map[string]any) {
			if f, ok := nodeFeed(cfg); ok {
				if _, seen := feeds[f.key()]; !seen {
					feeds[f.key()] = f
				}
			}
		})
	}
	for _, s := range settings {
		for _, src := range widgetSources(s.Value) {
			if f, ok := sourceFeed(src); ok {
				// A widget's own name for a feed is a better connection name
				// than the host a node's feed would be named by.
				if prev, seen := feeds[f.key()]; !seen || prev.label == feedLabel(prev.url) {
					feeds[f.key()] = f
				}
			}
		}
	}
	if len(feeds) == 0 {
		return
	}

	keys := make([]string, 0, len(feeds))
	for k := range feeds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	conns := map[string]string{}
	for _, k := range keys {
		id, err := createCalendarConnection(feeds[k])
		if err != nil {
			log.Error("calendar migration: cannot create a connection; its nodes and widgets keep their inline feed",
				zap.String("feed", feeds[k].url), zap.Error(err))
			continue
		}
		conns[k] = id
		log.Info("calendar migration: created a connection for an inline feed", zap.String("connection_id", id))
	}

	for _, v := range versions {
		if err := migrateVersion(v, conns); err != nil {
			log.Error("calendar migration: cannot rewrite a flow version; it keeps its inline feed",
				zap.String("flow_id", v.FlowID.String()), zap.Int("version", v.Version), zap.Error(err))
		}
	}
	for _, s := range settings {
		if err := migrateSources(s, conns); err != nil {
			log.Error("calendar migration: cannot rewrite widget sources; they keep their inline feeds",
				zap.String("key", s.Key), zap.Error(err))
		}
	}
}

// walkCalendarNodes calls fn for every calendar node config in a graph,
// including local subgraphs. fn may modify the config in place.
func walkCalendarNodes(g map[string]any, fn func(nodeType string, cfg map[string]any)) {
	nodes, _ := g["nodes"].([]any)
	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := n["type"].(string)
		if typ != calendarEventNode && typ != calendarEventsNode {
			continue
		}
		cfg, ok := n["config"].(map[string]any)
		if !ok {
			continue
		}
		fn(typ, cfg)
	}
	subs, _ := g["subgraphs"].(map[string]any)
	for _, sub := range subs {
		if sg, ok := sub.(map[string]any); ok {
			walkCalendarNodes(sg, fn)
		}
	}
}

// nodeFeed reads a calendar node's inline feed.
func nodeFeed(cfg map[string]any) (calendarFeed, bool) {
	u, _ := cfg["url"].(string)
	if strings.TrimSpace(u) == "" {
		return calendarFeed{}, false
	}
	provider, _ := cfg["provider"].(string)
	key, _ := cfg["api_key"].(string)
	return calendarFeed{provider: providerOrICS(provider), url: strings.TrimSpace(u), apiKey: key, label: feedLabel(u)}, true
}

// widgetSources decodes a widget's stored source list.
func widgetSources(raw string) []map[string]any {
	var list []map[string]any
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	return list
}

// sourceFeed reads a widget source's inline feed.
func sourceFeed(src map[string]any) (calendarFeed, bool) {
	f, ok := nodeFeed(src)
	if !ok {
		return f, false
	}
	if name, _ := src["name"].(string); strings.TrimSpace(name) != "" {
		f.label = strings.TrimSpace(name)
	}
	return f, true
}

// providerOrICS treats an unset provider as an ICS feed, the old default.
func providerOrICS(p string) string {
	if p == "" {
		return "ics"
	}
	return p
}

// feedLabel names a feed by its host, or by itself for a calendar ID.
func feedLabel(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSpace(raw)
}

// createCalendarConnection stores an encrypted connection for one feed, named
// "Calendar: <label>" with a numeric suffix when the name is taken.
func createCalendarConnection(f calendarFeed) (string, error) {
	kind := calendarICSKind
	cfg := map[string]any{"url": f.url}
	if f.provider == "google" {
		kind = calendarGoogleKind
		cfg = map[string]any{"calendars": f.url, "auth": "api_key", "api_key": f.apiKey}
	}
	base := "Calendar: " + f.label
	name := base
	for i := 2; ; i++ {
		var count int64
		if err := db.DB.Model(&models.Connection{}).Where("name = ?", name).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			break
		}
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	c := &models.Connection{Name: name, Kind: kind, Enabled: true}
	if err := c.SetConfig(cfg); err != nil {
		return "", err
	}
	if err := db.DB.Create(c).Error; err != nil {
		return "", err
	}
	return c.ID.String(), nil
}

// migrateVersion rewrites one flow version's calendar nodes to name their
// connections. A draft also gains the dependency rows its new connection
// fields imply, since the node metadata that would derive them belongs to a
// plugin that has not loaded yet.
func migrateVersion(v models.FlowVersion, conns map[string]string) error {
	var g map[string]any
	if err := json.Unmarshal([]byte(v.Graph), &g); err != nil {
		return err
	}
	changed := false
	var refs []string
	walkCalendarNodes(g, func(typ string, cfg map[string]any) {
		f, ok := nodeFeed(cfg)
		if !ok {
			return
		}
		id, ok := conns[f.key()]
		if !ok {
			return
		}
		rewriteNodeConfig(typ, cfg, id)
		refs = append(refs, id)
		changed = true
	})
	if !changed {
		return nil
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.FlowVersion{}).Where("id = ?", v.ID).Update("graph", string(raw)).Error; err != nil {
			return err
		}
		var f models.Flow
		if err := tx.Where("id = ?", v.FlowID).First(&f).Error; err != nil {
			return err
		}
		if f.DraftVersionID == nil || *f.DraftVersionID != v.ID {
			return nil
		}
		for _, id := range refs {
			var count int64
			if err := tx.Model(&models.FlowDependency{}).
				Where("flow_id = ? AND kind = ? AND ref_id = ?", f.ID, flow.DepConnection, id).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&models.FlowDependency{FlowID: f.ID, Kind: flow.DepConnection, RefID: id}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// rewriteNodeConfig replaces a node's inline feed with a connection. The old
// trigger fired a lead time before each start; that becomes a negative start
// offset.
func rewriteNodeConfig(typ string, cfg map[string]any, connectionID string) {
	cfg["connection"] = connectionID
	if typ == calendarEventNode {
		lead := 10.0
		if v, ok := cfg["lead_time_m"].(float64); ok {
			lead = v
		}
		cfg["edges"] = "start"
		if lead > 0 {
			cfg["start_offset"] = fmt.Sprintf("-%gm", lead)
		}
		delete(cfg, "window_days")
	}
	for _, k := range []string{"provider", "url", "api_key", "lead_time_m"} {
		delete(cfg, k)
	}
}

// migrateSources rewrites one widget's sources to name their connections.
func migrateSources(s models.Setting, conns map[string]string) error {
	list := widgetSources(s.Value)
	if list == nil {
		return nil
	}
	changed := false
	for _, src := range list {
		f, ok := sourceFeed(src)
		if !ok {
			continue
		}
		id, ok := conns[f.key()]
		if !ok {
			continue
		}
		src["connection_id"] = id
		for _, k := range []string{"provider", "url", "api_key"} {
			delete(src, k)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return db.DB.Model(&models.Setting{}).Where(clause.Eq{Column: settingKeyColumn, Value: s.Key}).Update("value", string(raw)).Error
}
