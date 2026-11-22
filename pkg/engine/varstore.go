package engine

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// Variable scopes, re-exported from pkg/flow so engine code and node code read
// the same names. The message scope is handled by the node holding the
// message, so only the run, flow, and global stores reach the engine.
const (
	ScopeMsg    = flow.ScopeMsg
	ScopeRun    = flow.ScopeRun
	ScopeFlow   = flow.ScopeFlow
	ScopeGlobal = flow.ScopeGlobal
)

// ValidScope reports whether s names a scope.
func ValidScope(s string) bool { return flow.ValidScope(s) }

// varBuffer is the queue depth of the persistence writer; writes beyond it are
// dropped with a warning rather than blocking a running flow.
const varBuffer = 1024

// VarSink persists flow and global variables. All methods must be
// non-blocking; implementations write asynchronously, so the in-memory value
// is authoritative for the running engine and the store is a durable copy.
type VarSink interface {
	// Save writes a variable's JSON encoding.
	Save(scope string, flowID uuid.UUID, key, value string)
	// Delete removes a variable.
	Delete(scope string, flowID uuid.UUID, key string)
	// Close flushes queued writes and stops the writer.
	Close()
}

// varStore holds flow and global variables in memory and mirrors every write
// to a sink. Reads never touch the database: a flow reading a counter on every
// message cannot afford a query, and the cache is the only writer.
type varStore struct {
	mu     sync.RWMutex
	flow   map[uuid.UUID]map[string]any
	global map[string]any
	sink   VarSink
}

// newVarStore returns an empty store writing through to sink, which may be nil
// when variables should live only as long as the process (tests).
func newVarStore(sink VarSink) *varStore {
	return &varStore{
		flow:   make(map[uuid.UUID]map[string]any),
		global: make(map[string]any),
		sink:   sink,
	}
}

// load seeds the cache from persisted rows. Values that no longer decode are
// skipped rather than failing startup: a bad row should not keep the engine down.
func (s *varStore) load(rows []*models.Variable, log *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		var value any
		if err := json.Unmarshal([]byte(row.Value), &value); err != nil {
			if log != nil {
				log.Warn("skipping unreadable variable",
					zap.String("scope", row.Scope), zap.String("key", row.Key), zap.Error(err))
			}
			continue
		}
		switch row.Scope {
		case ScopeGlobal:
			s.global[row.Key] = value
		case ScopeFlow:
			byKey := s.flow[row.FlowID]
			if byKey == nil {
				byKey = make(map[string]any)
				s.flow[row.FlowID] = byKey
			}
			byKey[row.Key] = value
		}
	}
}

// get reads one variable.
func (s *varStore) get(scope string, flowID uuid.UUID, key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if scope == ScopeGlobal {
		v, ok := s.global[key]
		return v, ok
	}
	v, ok := s.flow[flowID][key]
	return v, ok
}

// set writes one variable, mirroring it to the sink. A nil value clears the
// key, matching how run variables treat nil. The sink write is queued under
// the lock, so two runs setting the same variable reach the database in the
// order memory took them.
func (s *varStore) set(scope string, flowID uuid.UUID, key string, value any) {
	// A value that cannot be encoded stays in memory but is not persisted;
	// the caller already has it, so failing the node here would be worse. The
	// row of the value it replaces is removed instead, so a restart finds the
	// variable unset rather than loading a value that was since overwritten.
	var encoded []byte
	persistable := true
	if value != nil && s.sink != nil {
		var err error
		if encoded, err = json.Marshal(value); err != nil {
			persistable = false
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if value == nil {
		if scope == ScopeGlobal {
			delete(s.global, key)
		} else {
			delete(s.flow[flowID], key)
		}
	} else if scope == ScopeGlobal {
		s.global[key] = value
	} else {
		byKey := s.flow[flowID]
		if byKey == nil {
			byKey = make(map[string]any)
			s.flow[flowID] = byKey
		}
		byKey[key] = value
	}

	if s.sink == nil {
		return
	}
	if scope == ScopeGlobal {
		flowID = uuid.Nil
	}
	if value == nil || !persistable {
		s.sink.Delete(scope, flowID, key)
		return
	}
	s.sink.Save(scope, flowID, key, string(encoded))
}

// snapshot copies one scope's variables so expression evaluation and script
// execution never read a map another run is writing.
func (s *varStore) snapshot(scope string, flowID uuid.UUID) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.global
	if scope != ScopeGlobal {
		src = s.flow[flowID]
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// DBVarSink persists variables from a single writer goroutine fed by a
// buffered channel, so a flow writing a variable never waits on the database.
type DBVarSink struct {
	log    *zap.Logger
	ch     chan func()
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

// NewDBVarSink creates the sink and starts its writer goroutine.
func NewDBVarSink(log *zap.Logger) *DBVarSink {
	s := &DBVarSink{log: log, ch: make(chan func(), varBuffer)}
	s.wg.Add(1)
	go s.writer()
	return s
}

// writer drains queued write operations until the channel closes.
func (s *DBVarSink) writer() {
	defer s.wg.Done()
	for op := range s.ch {
		op()
	}
}

// enqueue submits a write, dropping it if the queue is full or the sink has
// closed. The in-memory value already reflects the change either way.
func (s *DBVarSink) enqueue(op func()) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- op:
	default:
		s.log.Warn("variable write queue full, dropping write")
	}
}

// Save persists a variable value.
func (s *DBVarSink) Save(scope string, flowID uuid.UUID, key, value string) {
	s.enqueue(func() {
		if err := models.UpsertVariable(scope, flowID, key, value); err != nil {
			s.log.Error("failed to persist variable",
				zap.String("scope", scope), zap.String("key", key), zap.Error(err))
		}
	})
}

// Delete removes a persisted variable.
func (s *DBVarSink) Delete(scope string, flowID uuid.UUID, key string) {
	s.enqueue(func() {
		if err := models.DeleteVariable(scope, flowID, key); err != nil {
			s.log.Error("failed to delete variable",
				zap.String("scope", scope), zap.String("key", key), zap.Error(err))
		}
	})
}

// Close flushes queued writes and stops the writer.
func (s *DBVarSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.ch)
	s.mu.Unlock()
	s.wg.Wait()
}
