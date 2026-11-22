package models

import (
	"sync"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// List topics announce that a list page's contents changed. The notice
// carries nothing: a page that hears it reads the list again through the API,
// which applies the reader's shares, so the notice reveals only that something
// changed.
const (
	FlowsListTopic      = "flows"
	DashboardsListTopic = "dashboards"
)

// listChangeDelay coalesces a burst of writes, such as an import or a folder
// move that touches every flow in it, into one notice. It also lets the
// transaction that made the write commit before a page reads the list again.
const listChangeDelay = 250 * time.Millisecond

// listTables maps each table a list page draws from to its list topic. Shares
// are included because sharing or unsharing adds a row to or removes one from
// someone's list.
var listTables = map[any]string{
	&Flow{}:                 FlowsListTopic,
	&Folder{}:               FlowsListTopic,
	&FlowAccess{}:           FlowsListTopic,
	&FolderAccess{}:         FlowsListTopic,
	&Dashboard{}:            DashboardsListTopic,
	&DashboardGroup{}:       DashboardsListTopic,
	&DashboardAccess{}:      DashboardsListTopic,
	&DashboardGroupAccess{}: DashboardsListTopic,
}

// listNotifier publishes one list notice per topic per burst of writes.
type listNotifier struct {
	publish func(topic string)
	mu      sync.Mutex
	pending map[string]bool
}

// WatchListChanges announces every create, update, and delete on the tables
// the flow and dashboard lists draw from, whichever path made it: REST, MCP,
// import, or restore. publish is called once per topic after a burst settles.
func WatchListChanges(publish func(topic string)) error {
	tables := make(map[string]string, len(listTables))
	for model, topic := range listTables {
		stmt := &gorm.Statement{DB: db.DB}
		if err := stmt.Parse(model); err != nil {
			return err
		}
		tables[stmt.Schema.Table] = topic
	}

	n := &listNotifier{publish: publish, pending: make(map[string]bool)}
	after := func(tx *gorm.DB) {
		if tx.Error != nil || tx.RowsAffected == 0 {
			return
		}
		if topic, ok := tables[tx.Statement.Table]; ok {
			n.mark(topic)
		}
	}

	// Register after each operation's own work.
	cb := db.DB.Callback()
	if err := cb.Create().After("gorm:create").Register("karakuri:list_changes", after); err != nil {
		return err
	}
	if err := cb.Update().After("gorm:update").Register("karakuri:list_changes", after); err != nil {
		return err
	}
	return cb.Delete().After("gorm:delete").Register("karakuri:list_changes", after)
}

// mark schedules a topic's notice unless one is already waiting.
func (n *listNotifier) mark(topic string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending[topic] {
		return
	}
	n.pending[topic] = true
	time.AfterFunc(listChangeDelay, func() {
		n.mu.Lock()
		delete(n.pending, topic)
		n.mu.Unlock()
		n.publish(topic)
	})
}
