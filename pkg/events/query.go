package events

import (
	"errors"
	"strings"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// Page limits.
const (
	DefaultLimit = 100
	MaxLimit     = 500
	// searchCap bounds a text search to the newest matching rows the other
	// filters leave, so a search over a large log stays an index walk.
	searchCap = 10000
)

// ErrAuditRequired reports a request for security entries without the audit
// permission.
var ErrAuditRequired = errors.New("security entries need the audit permission")

// Filter selects entries.
type Filter struct {
	Categories []string
	Levels     []string
	Subjects   []Subject
	ActorKind  string
	ActorID    string
	Text       string
	From, To   time.Time
	// Before is the ID the page starts below; zero is the newest.
	Before uint
	Limit  int
	// Audit includes security entries.
	Audit bool
	// SubjectAllowed, when set, restricts the query to subjects it allows and
	// never returns security entries: a dashboard viewer reading through the
	// widgets the dashboard shows.
	SubjectAllowed func(Subject) bool
	// LimitFlows hides flow and folder entries except those whose subject ID
	// is in ReadableFlows or ReadableFolders: a user sees the log of the flows
	// and folders they may read.
	LimitFlows      bool
	ReadableFlows   []string
	ReadableFolders []string
	// LimitConnections hides connection, device, and entity entries except
	// those on a connection in UsableConnections or a device in
	// UsableDevices. An entity entry's ID is <connection>/<entity>.
	LimitConnections  bool
	UsableConnections []string
	UsableDevices     []string
	// LimitAgents hides agent entries except those in UsableAgents.
	LimitAgents  bool
	UsableAgents []string
}

// orNone returns the IDs, or a value no ID has, so an IN clause over an empty
// list stays valid SQL.
func orNone(ids []string) []string {
	if len(ids) == 0 {
		return []string{""}
	}
	return ids
}

// ParseSubject reads kind:id.
func ParseSubject(s string) (Subject, bool) {
	kind, id, ok := strings.Cut(s, ":")
	if !ok || kind == "" || id == "" {
		return Subject{}, false
	}
	return Subject{Kind: kind, ID: id}, true
}

// Page is one page of entries, newest first.
type Page struct {
	Events     []models.Event `json:"events" description:"Entries, newest first."`
	NextBefore uint           `json:"next_before,omitempty" description:"Pass as before for the next page; absent on the last."`
}

// List answers one page of entries matching f.
func List(f Filter) (Page, error) {
	return listOn(db.DB, f)
}

func listOn(gdb *gorm.DB, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	scoped := f.SubjectAllowed != nil
	for _, c := range f.Categories {
		if c == models.EventSecurity && (!f.Audit || scoped) {
			return Page{}, ErrAuditRequired
		}
	}
	if scoped {
		if len(f.Subjects) == 0 {
			return Page{}, errors.New("a dashboard reads entries only for the subjects it shows")
		}
		for _, s := range f.Subjects {
			if !f.SubjectAllowed(s) {
				return Page{}, errors.New("this dashboard does not show " + s.Kind + " " + s.ID)
			}
		}
	}

	q := gdb.Model(&models.Event{})
	if !f.Audit || scoped {
		q = q.Where("category <> ?", models.EventSecurity)
	}
	if len(f.Categories) > 0 {
		q = q.Where("category IN ?", f.Categories)
	}
	if len(f.Levels) > 0 {
		q = q.Where("level IN ?", f.Levels)
	}
	if len(f.Subjects) > 0 {
		or := gdb.Where("1 = 0")
		for _, s := range f.Subjects {
			or = or.Or("subject_kind = ? AND subject_id = ?", s.Kind, s.ID)
		}
		q = q.Where(or)
	}
	if f.LimitFlows {
		flows, folders := f.ReadableFlows, f.ReadableFolders
		if len(flows) == 0 {
			flows = []string{""}
		}
		if len(folders) == 0 {
			folders = []string{""}
		}
		q = q.Where("(subject_kind NOT IN ? OR (subject_kind = ? AND subject_id IN ?) OR (subject_kind = ? AND subject_id IN ?))",
			[]string{"flow", "folder"}, "flow", flows, "folder", folders)
	}
	if f.LimitConnections {
		conns := orNone(f.UsableConnections)
		q = q.Where("(subject_kind NOT IN ? OR (subject_kind = ? AND subject_id IN ?) OR (subject_kind = ? AND subject_id IN ?) OR (subject_kind = ? AND SUBSTR(subject_id, 1, 36) IN ?))",
			[]string{"connection", "device", "ha_entity"}, "connection", conns, "device", orNone(f.UsableDevices), "ha_entity", conns)
	}
	if f.LimitAgents {
		q = q.Where("(subject_kind <> ? OR subject_id IN ?)", "agent", orNone(f.UsableAgents))
	}
	if f.ActorKind != "" {
		q = q.Where("actor_kind = ?", f.ActorKind)
		if f.ActorID != "" {
			q = q.Where("actor_id = ?", f.ActorID)
		}
	}
	if !f.From.IsZero() {
		q = q.Where("at >= ?", f.From.UTC())
	}
	if !f.To.IsZero() {
		q = q.Where("at <= ?", f.To.UTC())
	}
	if f.Before > 0 {
		q = q.Where("id < ?", f.Before)
	}
	if text := strings.TrimSpace(f.Text); text != "" {
		// Restrict the search to the newest rows the other filters leave,
		// then match within them.
		var floor struct{ ID uint }
		sub := q.Session(&gorm.Session{}).Select("id").Order("id DESC").Offset(searchCap - 1).Limit(1)
		if err := sub.Scan(&floor).Error; err != nil {
			return Page{}, err
		}
		like := "%" + escapeLike(text) + "%"
		q = q.Where("(message LIKE ? ESCAPE '!' OR subject_name LIKE ? ESCAPE '!' OR actor_name LIKE ? ESCAPE '!')", like, like, like)
		if floor.ID > 0 {
			q = q.Where("id >= ?", floor.ID)
		}
	}

	var rows []models.Event
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return Page{}, err
	}
	page := Page{Events: rows}
	if len(rows) > limit {
		page.Events = rows[:limit]
		page.NextBefore = rows[limit-1].ID
	}
	if page.Events == nil {
		page.Events = []models.Event{}
	}
	return page, nil
}

// escapeLike escapes LIKE wildcards with !, which every engine reads the
// same way inside a quoted string, unlike a backslash on MySQL.
func escapeLike(s string) string {
	r := strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)
	return r.Replace(s)
}
