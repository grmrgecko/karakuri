package events

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: path, MaxOpenConns: 4, MaxIdleConns: 4}))
	t.Cleanup(func() {
		if s, err := db.DB.DB(); err == nil {
			s.Close()
		}
	})
	require.NoError(t, db.DB.AutoMigrate(&models.Event{}, &models.Setting{}))
	apply(Defaults())
}

// fakePub records what was published.
type fakePub struct {
	mu   sync.Mutex
	msgs []string
}

func (p *fakePub) Publish(topic, msgType string, _ interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, topic+" "+msgType)
}

func (p *fakePub) count(s string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, m := range p.msgs {
		if m == s {
			n++
		}
	}
	return n
}

// TestRepeatsFold covers FR-006 and SC-003: a hundred identical failures in a
// minute become one entry with a count, published once as new and then as
// updates.
func TestRepeatsFold(t *testing.T) {
	setupDB(t)
	pub := &fakePub{}
	r := NewRecorder(pub, nil)
	r.Start()
	base := time.Now()
	for i := 0; i < 100; i++ {
		r.Record(Entry{Category: models.EventConnection, Level: models.EventError,
			Subject: Subject{Kind: "connection", ID: "c1", Name: "Garage"}, Message: "Garage disconnected: refused",
			At: base.Add(time.Duration(i) * 100 * time.Millisecond)})
	}
	r.Record(Entry{Category: models.EventConnection, Level: models.EventInfo,
		Subject: Subject{Kind: "connection", ID: "c1", Name: "Garage"}, Message: "Garage connected", At: base.Add(20 * time.Second)})
	r.Stop()

	var rows []models.Event
	require.NoError(t, db.DB.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, 100, rows[0].Count)
	assert.True(t, rows[0].LastAt.After(rows[0].At))
	assert.Equal(t, 2, pub.count("events event"), "the failure and the recovery are new entries")
	assert.Equal(t, 99, pub.count("events event_update"))
	assert.Equal(t, 2, pub.count("events:subject:connection:c1 event"))

	// A repeat after the window starts a new entry.
	r2 := NewRecorder(nil, nil)
	r2.Start()
	r2.Record(Entry{Category: models.EventCustom, Message: "tick", At: base})
	r2.Record(Entry{Category: models.EventCustom, Message: "tick", At: base.Add(61 * time.Second)})
	r2.Stop()
	var n int64
	db.DB.Model(&models.Event{}).Where("message = ?", "tick").Count(&n)
	assert.Equal(t, int64(2), n)
}

// TestInterleavedChangesDoNotFold covers the porch light at 3 a.m.: on, off,
// and on again are three entries, and the same message from someone else, or
// from another address, is its own entry, so a count never hides who did what
// in between.
func TestInterleavedChangesDoNotFold(t *testing.T) {
	setupDB(t)
	r := NewRecorder(nil, nil)
	r.Start()
	porch := Subject{Kind: "control", ID: "p", Name: "Porch light"}
	sam := Actor{Kind: ActorUser, ID: "sam", Name: "sam"}
	flow := Actor{Kind: ActorFlow, ID: "f", Name: "Motion lights"}
	base := time.Now()
	r.Record(Entry{Category: models.EventDevice, Subject: porch, Message: "Porch light set to on", Actor: flow, At: base})
	r.Record(Entry{Category: models.EventDevice, Subject: porch, Message: "Porch light set to off", Actor: sam, At: base.Add(time.Second)})
	r.Record(Entry{Category: models.EventDevice, Subject: porch, Message: "Porch light set to on", Actor: flow, At: base.Add(2 * time.Second)})
	r.Record(Entry{Category: models.EventDevice, Subject: porch, Message: "Porch light set to on", Actor: sam, At: base.Add(3 * time.Second)})
	r.Record(Entry{Category: models.EventDevice, Subject: porch, Message: "Porch light set to on", Actor: sam, At: base.Add(4 * time.Second)})
	r.Stop()

	var rows []models.Event
	require.NoError(t, db.DB.Order("id").Find(&rows).Error)
	require.Len(t, rows, 4)
	assert.Equal(t, []int{1, 1, 1, 2}, []int{rows[0].Count, rows[1].Count, rows[2].Count, rows[3].Count})

	// Failed sign-ins from two addresses keep both addresses.
	r = NewRecorder(nil, nil)
	r.Start()
	ada := Subject{Kind: "user", Name: "ada"}
	for i, addr := range []string{"10.0.0.5", "203.0.113.9", "203.0.113.9"} {
		r.Record(Entry{Category: models.EventSecurity, Subject: ada, Message: "Failed sign-in for ada",
			Actor: Actor{Kind: ActorAnonymous, Name: "ada", Address: addr}, At: base.Add(time.Duration(5+i) * time.Second)})
	}
	r.Stop()
	rows = nil
	require.NoError(t, db.DB.Where("subject_kind = ?", "user").Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, "10.0.0.5", rows[0].Address)
	assert.Equal(t, "203.0.113.9", rows[1].Address)
	assert.Equal(t, 2, rows[1].Count)
}

// TestFullQueueDrops covers FR-013: recording never waits on the log.
func TestFullQueueDrops(t *testing.T) {
	setupDB(t)
	r := NewRecorder(nil, nil)
	start := time.Now()
	for i := 0; i < queueSize+500; i++ {
		r.Record(Entry{Category: models.EventCustom, Message: fmt.Sprint(i)})
	}
	assert.Less(t, time.Since(start), time.Second, "recording does not block on a stopped writer")
	assert.Equal(t, int64(500), r.dropped.Load())
}

// TestLimitsAndSecrets covers the size limits on one entry.
func TestLimitsAndSecrets(t *testing.T) {
	big := map[string]any{"blob": string(make([]byte, maxDetailBytes+1))}
	ev := toModel(Entry{Category: models.EventCustom, Message: string(make([]rune, 600)), Details: big}, time.Now())
	assert.Len(t, []rune(ev.Message), maxMessage)
	assert.Equal(t, map[string]any{"truncated": true}, ev.Details)
	assert.Equal(t, ActorSystem, ev.ActorKind, "an entry without an actor is the system's")
}

func insert(t *testing.T, e models.Event) models.Event {
	t.Helper()
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.LastAt, e.Count = e.At, 1
	if e.Level == "" {
		e.Level = models.EventInfo
	}
	if e.ActorKind == "" {
		e.ActorKind = ActorSystem
	}
	require.NoError(t, db.DB.Create(&e).Error)
	return e
}

// TestListVisibilityAndPages covers FR-007 and FR-008: security entries are
// invisible without audit and refused when named, dashboard reads are
// limited to their subjects, and pages stay stable while entries arrive.
func TestListVisibilityAndPages(t *testing.T) {
	setupDB(t)
	for i := 0; i < 250; i++ {
		insert(t, models.Event{Category: models.EventDevice, SubjectKind: "control", SubjectID: fmt.Sprint(i % 3), Message: fmt.Sprintf("change %d", i)})
	}
	insert(t, models.Event{Category: models.EventSecurity, Level: models.EventWarning, Message: "Failed sign-in for admin"})

	page, err := List(Filter{})
	require.NoError(t, err)
	require.Len(t, page.Events, DefaultLimit)
	assert.Equal(t, "change 249", page.Events[0].Message, "newest first, security hidden")
	insert(t, models.Event{Category: models.EventDevice, Message: "arrives between pages"})
	next, err := List(Filter{Before: page.NextBefore})
	require.NoError(t, err)
	assert.Equal(t, "change 149", next.Events[0].Message, "a new entry does not shift the next page")

	_, err = List(Filter{Categories: []string{models.EventSecurity}})
	assert.ErrorIs(t, err, ErrAuditRequired)
	audit, err := List(Filter{Categories: []string{models.EventSecurity}, Audit: true})
	require.NoError(t, err)
	assert.Len(t, audit.Events, 1)

	bySubject, err := List(Filter{Subjects: []Subject{{Kind: "control", ID: "1"}}, Limit: 500})
	require.NoError(t, err)
	assert.Len(t, bySubject.Events, 83)

	allow := func(s Subject) bool { return s.Kind == "control" && s.ID == "1" }
	_, err = List(Filter{SubjectAllowed: allow})
	assert.Error(t, err, "a dashboard must name its subjects")
	_, err = List(Filter{SubjectAllowed: allow, Subjects: []Subject{{Kind: "control", ID: "2"}}})
	assert.Error(t, err, "a dashboard cannot read a subject it does not show")
	_, err = List(Filter{SubjectAllowed: allow, Audit: true, Subjects: []Subject{{Kind: "control", ID: "1"}}, Categories: []string{models.EventSecurity}})
	assert.ErrorIs(t, err, ErrAuditRequired, "a dashboard never reads security entries")

	found, err := List(Filter{Text: "change 24"})
	require.NoError(t, err)
	assert.Len(t, found.Events, 11, "change 24 and change 240 to 249")
	pct, err := List(Filter{Text: "100%"})
	require.NoError(t, err)
	assert.Empty(t, pct.Events, "a wildcard in the search is literal")
}

// TestSweepKeepsSecurity covers FR-011: age per category, then the oldest
// non-security entries past the limit.
func TestSweepKeepsSecurity(t *testing.T) {
	setupDB(t)
	now := time.Now()
	insert(t, models.Event{Category: models.EventDevice, Message: "old", At: now.AddDate(0, 0, -31)})
	insert(t, models.Event{Category: models.EventSecurity, Message: "old sign-in", At: now.AddDate(0, 0, -200)})
	insert(t, models.Event{Category: models.EventSecurity, Message: "ancient sign-in", At: now.AddDate(0, 0, -400)})
	for i := 0; i < 20; i++ {
		insert(t, models.Event{Category: models.EventDevice, Message: fmt.Sprint(i)})
	}
	s := Defaults()
	s.MaxRows = 10
	apply(s)
	removed, err := Sweep(now)
	require.NoError(t, err)
	assert.Equal(t, int64(13), removed, "two by age, then eleven over the limit")

	var left []models.Event
	require.NoError(t, db.DB.Order("id").Find(&left).Error)
	assert.Len(t, left, 10)
	assert.Equal(t, "old sign-in", left[0].Message, "security entries are not removed for room")
}

// TestListAtAMillion covers SC-004: the newest 100 entries, filtered, answer
// in under 200 ms with a million stored.
func TestListAtAMillion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a million rows")
	}
	setupDB(t)
	require.NoError(t, db.DB.Exec(`
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000000)
		INSERT INTO events (at, last_at, count, category, level, subject_kind, subject_id, subject_name, message, actor_kind)
		SELECT datetime('now', '-' || (1000000 - i) || ' seconds'), datetime('now'), 1,
		       CASE i % 7 WHEN 0 THEN 'connection' WHEN 1 THEN 'flow' ELSE 'device' END,
		       CASE i % 11 WHEN 0 THEN 'error' ELSE 'info' END,
		       'control', 'c' || (i % 500), 'Control ' || (i % 500), 'Control set to ' || i, 'system'
		FROM n`).Error)

	for _, f := range []Filter{
		{Categories: []string{models.EventConnection}},
		{Subjects: []Subject{{Kind: "control", ID: "c42"}}},
		{Levels: []string{models.EventError}, Categories: []string{models.EventFlow}},
	} {
		start := time.Now()
		page, err := List(f)
		took := time.Since(start)
		require.NoError(t, err)
		assert.Len(t, page.Events, 100)
		assert.Less(t, took, 200*time.Millisecond, "filter %+v took %s", f, took)
	}
}
