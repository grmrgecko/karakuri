package memories

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// Embedding is asynchronous on the write path: Remember returns as soon as
// the row is stored, and this worker fills vectors in behind it. A memory
// recalls by keywords until its vector lands, which is the honest degradation
// — an agent should not wait out a provider call to be told "remembered".

// EmbedFunc produces one vector per text through a named AI provider
// connection. Injected by serve.go over connops so this package never links
// the connection manager.
type EmbedFunc func(ctx context.Context, connection, model string, texts []string) ([][]float32, error)

// embedBatch is how many pending memories one provider call carries.
const embedBatch = 16

// embedQueueDepth bounds the pending queue. A full queue drops the enqueue —
// the memory is stored either way, and the next Load backfills whatever was
// dropped.
const embedQueueDepth = 4096

// sweepInterval is how often expired memories are physically deleted.
const sweepInterval = time.Hour

// embedWorker owns the pending queue and the injected embed function.
type embedWorker struct {
	mu      sync.Mutex
	embedFn EmbedFunc
	queue   chan uuid.UUID
}

func (w *embedWorker) init() {
	w.queue = make(chan uuid.UUID, embedQueueDepth)
}

func (w *embedWorker) fn() EmbedFunc {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.embedFn
}

// enqueue queues memories for embedding, dropping when the queue is full.
func (w *embedWorker) enqueue(ids ...uuid.UUID) {
	for _, id := range ids {
		select {
		case w.queue <- id:
		default:
			return
		}
	}
}

// SetEmbedFunc wires the provider call the worker and query embedding use.
func (s *Store) SetEmbedFunc(fn EmbedFunc) {
	s.embed.mu.Lock()
	s.embed.embedFn = fn
	s.embed.mu.Unlock()
}

// Start runs the background embedder and the expiry sweeper until ctx ends.
func (s *Store) Start(ctx context.Context, log *zap.Logger) {
	if log == nil {
		log = zap.NewNop()
	}
	go s.runEmbedder(ctx, log)
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Sweep(); err != nil {
					log.Error("memory sweep failed", zap.Error(err))
				}
			}
		}
	}()
}

// runEmbedder drains the pending queue in batches. A provider failure drops
// the batch and logs it — the memories stay recallable by keywords, and the
// next Load re-queues everything still missing a vector.
func (s *Store) runEmbedder(ctx context.Context, log *zap.Logger) {
	for {
		var first uuid.UUID
		select {
		case <-ctx.Done():
			return
		case first = <-s.embed.queue:
		}

		batch := []uuid.UUID{first}
	drain:
		for len(batch) < embedBatch {
			select {
			case id := <-s.embed.queue:
				batch = append(batch, id)
			default:
				break drain
			}
		}
		s.embedBatch(ctx, batch, log)
	}
}

// pendingText is one memory resolved for embedding, grouped by the provider
// call that will serve it.
type pendingText struct {
	id   uuid.UUID
	text string
	// memText and tags are the memory as it stood when its text was taken,
	// which the result must still match to be stored.
	memText string
	tags    string
}

// embedBatch resolves a batch against the cache, groups it by connection and
// model, and writes the vectors back.
func (s *Store) embedBatch(ctx context.Context, ids []uuid.UUID, log *zap.Logger) {
	type group struct {
		connection string
		model      string
		items      []pendingText
	}
	groups := map[string]*group{}

	s.mu.RLock()
	for _, id := range ids {
		for _, e := range s.stores {
			it, ok := e.items[id]
			if !ok {
				continue
			}
			// The store lost its embedding config, or the item was embedded
			// by an earlier duplicate enqueue.
			if e.def.EmbedConnection == "" || it.Embedded {
				break
			}
			key := e.def.EmbedConnection + "\x00" + e.def.EmbedModel
			g := groups[key]
			if g == nil {
				g = &group{connection: e.def.EmbedConnection, model: e.def.EmbedModel}
				groups[key] = g
			}
			g.items = append(g.items, pendingText{id: id, text: embedText(it), memText: it.Text, tags: encodeTags(it.Tags)})
			break
		}
	}
	s.mu.RUnlock()

	fn := s.embed.fn()
	if fn == nil {
		return
	}
	for _, g := range groups {
		texts := make([]string, len(g.items))
		for i, p := range g.items {
			texts[i] = p.text
		}
		vecs, err := fn(ctx, g.connection, g.model, texts)
		if err != nil {
			log.Warn("memory embedding failed",
				zap.String("connection", g.connection), zap.String("model", g.model),
				zap.Int("memories", len(texts)), zap.Error(err))
			continue
		}
		if len(vecs) != len(g.items) {
			log.Warn("memory embedding returned wrong count",
				zap.Int("want", len(g.items)), zap.Int("got", len(vecs)))
			continue
		}
		for i, p := range g.items {
			s.storeVector(p, g.connection, g.model, vecs[i], log)
		}
	}
}

// storeVector keeps a provider's vector only if it still describes the memory:
// the text and tags it was computed from, embedded with the store's current
// connection and model. An edit or a model change made while the provider was
// answering has already invalidated the memory and queued it again; writing the
// late result would mark the stale vector current, and a reload would trust it.
// The check and both writes hold the store lock so an edit cannot land between
// them, and the table write is itself conditional on the row's text and tags.
func (s *Store) storeVector(p pendingText, connection, model string, vec []float32, log *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.stores {
		it, ok := e.items[p.id]
		if !ok {
			continue
		}
		if it.Embedded || e.def.EmbedConnection != connection || e.def.EmbedModel != model ||
			it.Text != p.memText || encodeTags(it.Tags) != p.tags {
			log.Debug("discarding a memory embedding that no longer matches", zap.String("memory", p.id.String()))
			return
		}
		stored, err := models.UpdateMemoryEmbeddingIf(p.id, vectorBytes(vec), p.memText, p.tags)
		if err != nil {
			log.Error("storing memory embedding failed", zap.Error(err))
			return
		}
		if !stored {
			log.Debug("discarding a memory embedding whose row changed", zap.String("memory", p.id.String()))
			return
		}
		it.vector = vec
		it.Embedded = true
		return
	}
}

// embedText is what a memory embeds as: its text, with tags folded in so a
// tag-only match has a vector-side signal too.
func embedText(it *item) string {
	if len(it.Tags) == 0 {
		return it.Text
	}
	out := it.Text
	for _, tag := range it.Tags {
		out += "\n" + tag
	}
	return out
}

// vectorBytes encodes a vector as little-endian float32 bytes for storage.
func vectorBytes(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(f))
	}
	return out
}

// bytesVector decodes a stored vector, tolerating an odd length as absent.
func bytesVector(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}
