package agenthub

import (
	"sync"

	"github.com/google/uuid"
)

// AttachLock exposes the per-agent connect lock, so a test can hold a connect
// between its enabled check and its attach.
func (h *Hub) AttachLock(id uuid.UUID) *sync.Mutex { return h.attachLock(id) }
