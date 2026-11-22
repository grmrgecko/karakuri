package memories

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Recall scoring blends three relevance signals — vector similarity, keyword
// overlap, and importance — then decays the blend by age. The split follows
// what hybrid memory systems converge on: similarity carries half the weight,
// keywords a third, importance the rest, and when a vector is missing (no
// embedding connection, or one not computed yet) its weight redistributes over
// the other two rather than zeroing the memory out.
const (
	weightVector     = 0.5
	weightKeyword    = 0.3
	weightImportance = 0.2

	// defaultRecallLimit bounds a recall that named no limit.
	defaultRecallLimit = 5

	// MMR considers more hybrid matches than it returns, without allowing a
	// small recall to turn into an unbounded pairwise comparison.
	mmrRelevanceWeight  = 0.7
	mmrCandidateFactor  = 4
	maxMMRCandidatePool = 100

	// minRecallScore drops results that only recency could love: with no
	// relevance signal at all, a memory does not belong in an answer about
	// something else.
	minRecallScore = 0.05

	// queryEmbedTimeout bounds the synchronous query embedding; past it the
	// recall proceeds on keywords rather than holding the agent's loop.
	queryEmbedTimeout = 10 * time.Second
)

// RerankMMR selects maximal marginal relevance reranking.
const RerankMMR = "mmr"

// RecallOpts shapes one recall.
type RecallOpts struct {
	// Session widens the search to memories scoped to this session beside the
	// global ones; a session-scoped fact never leaks into another session's
	// recall.
	Session string
	// Limit caps the results; 0 means the default.
	Limit int
	// IncludeTurns searches conversation turns beside facts, which is how
	// "what did I say about the boiler" reaches last month's transcript.
	IncludeTurns bool
	// Rerank names an optional reranker. "mmr" trades some relevance for
	// diversity to suppress near-duplicate memories.
	Rerank string
}

// recallCandidate keeps the private similarity inputs beside the public
// result while MMR builds its result set.
type recallCandidate struct {
	result Result
	vector []float32
	tokens map[string]struct{}
}

// Recall returns the memories most relevant to a query, best first. The query
// is embedded synchronously when the store has an embedding connection; an
// embedding failure degrades to keyword scoring rather than failing the
// recall, because a dead embedding endpoint should not lobotomize an agent
// mid-conversation.
func (s *Store) Recall(ctx context.Context, storeID uuid.UUID, query string, opts RecallOpts) []Result {
	queryTokens := tokenize(query)
	queryVector := s.queryVector(ctx, storeID, query)

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultRecallLimit
	}

	now := time.Now()
	s.mu.RLock()
	e, ok := s.stores[storeID]
	if !ok {
		s.mu.RUnlock()
		return nil
	}
	halfLife := e.def.HalfLifeDays
	recencyWeight := e.def.RecencyWeight
	var scored []recallCandidate
	for _, it := range e.items {
		if expired(it, now) {
			continue
		}
		if it.Kind == models.MemoryKindTurn && !opts.IncludeTurns {
			continue
		}
		if it.Session != "" && it.Session != opts.Session {
			continue
		}
		score := scoreItem(it, queryTokens, queryVector, now, halfLife, recencyWeight)
		if score < minRecallScore {
			continue
		}
		scored = append(scored, recallCandidate{
			result: Result{Item: it.Item, Score: score},
			vector: it.vector,
			tokens: it.tokens,
		})
	}
	s.mu.RUnlock()

	sort.Slice(scored, func(i, j int) bool { return scored[i].result.Score > scored[j].result.Score })
	if len(scored) == 0 {
		return nil
	}
	if opts.Rerank == RerankMMR {
		return rerankMMR(scored, limit)
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]Result, len(scored))
	for i := range scored {
		results[i] = scored[i].result
	}
	return results
}

// rerankMMR applies maximal marginal relevance to the leading hybrid matches.
// Result scores stay unchanged because callers expose them as hybrid relevance.
func rerankMMR(scored []recallCandidate, limit int) []Result {
	poolLimit := min(limit*mmrCandidateFactor, maxMMRCandidatePool)
	if len(scored) > poolLimit {
		pool := scored[:poolLimit]
		selected := selectMMR(pool, min(limit, poolLimit))
		if limit > poolLimit {
			for _, candidate := range scored[poolLimit:min(limit, len(scored))] {
				selected = append(selected, candidate.result)
			}
		}
		return selected
	}
	if limit > len(scored) {
		limit = len(scored)
	}
	return selectMMR(scored, limit)
}

func selectMMR(scored []recallCandidate, limit int) []Result {
	selected := make([]recallCandidate, 0, limit)
	remaining := append([]recallCandidate(nil), scored...)
	for len(selected) < limit {
		best := 0
		bestScore := math.Inf(-1)
		for i := range remaining {
			maxSimilarity := 0.0
			for j := range selected {
				maxSimilarity = math.Max(maxSimilarity, memorySimilarity(remaining[i], selected[j]))
			}
			mmrScore := mmrRelevanceWeight*remaining[i].result.Score -
				(1-mmrRelevanceWeight)*maxSimilarity
			if mmrScore > bestScore {
				best, bestScore = i, mmrScore
			}
		}
		selected = append(selected, remaining[best])
		remaining = append(remaining[:best], remaining[best+1:]...)
	}

	results := make([]Result, len(selected))
	for i := range selected {
		results[i] = selected[i].result
	}
	return results
}

// memorySimilarity uses semantic similarity when both memories share a vector
// space, and token overlap for memories without compatible vectors.
func memorySimilarity(a, b recallCandidate) float64 {
	if similarity, ok := cosine(a.vector, b.vector); ok {
		return math.Max(0, similarity)
	}
	if len(a.tokens) == 0 || len(b.tokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range a.tokens {
		if _, ok := b.tokens[token]; ok {
			intersection++
		}
	}
	union := len(a.tokens) + len(b.tokens) - intersection
	return float64(intersection) / float64(union)
}

// Browse is the settings page's read: with a query it scores like Recall but
// across every session and kind filter it was given; without one it lists
// newest first. Sessionless browsing sees everything, because the operator
// editing the bank is not one conversation among many.
func (s *Store) Browse(ctx context.Context, storeID uuid.UUID, query, session, kind string, limit int) []Result {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now()
	queryTokens := tokenize(query)
	var queryVector []float32
	if len(queryTokens) > 0 {
		queryVector = s.queryVector(ctx, storeID, query)
	}

	s.mu.RLock()
	e, ok := s.stores[storeID]
	if !ok {
		s.mu.RUnlock()
		return nil
	}
	halfLife := e.def.HalfLifeDays
	recencyWeight := e.def.RecencyWeight
	var out []Result
	for _, it := range e.items {
		if expired(it, now) {
			continue
		}
		if kind != "" && it.Kind != kind {
			continue
		}
		if session != "" && it.Session != session {
			continue
		}
		r := Result{Item: it.Item}
		if len(queryTokens) > 0 {
			r.Score = scoreItem(it, queryTokens, queryVector, now, halfLife, recencyWeight)
			if r.Score < minRecallScore {
				continue
			}
		}
		out = append(out, r)
	}
	// Sorting reads seq under the lock; the slice itself holds copies.
	seqs := make(map[uuid.UUID]int64, len(out))
	for _, r := range out {
		if it, ok := e.items[r.ID]; ok {
			seqs[r.ID] = it.seq
		}
	}
	s.mu.RUnlock()

	if len(queryTokens) > 0 {
		sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	} else {
		sort.Slice(out, func(i, j int) bool { return seqs[out[i].ID] > seqs[out[j].ID] })
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// queryVector embeds a recall query when the store can, or returns nil so
// scoring proceeds on keywords.
func (s *Store) queryVector(ctx context.Context, storeID uuid.UUID, query string) []float32 {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	s.mu.RLock()
	e, ok := s.stores[storeID]
	var conn, model string
	if ok {
		conn, model = e.def.EmbedConnection, e.def.EmbedModel
	}
	s.mu.RUnlock()
	if !ok || conn == "" {
		return nil
	}
	fn := s.embed.fn()
	if fn == nil {
		return nil
	}
	embedCtx, done := context.WithTimeout(ctx, queryEmbedTimeout)
	defer done()
	vecs, err := fn(embedCtx, conn, model, []string{query})
	if err != nil || len(vecs) != 1 {
		return nil
	}
	return vecs[0]
}

// scoreItem computes one memory's recall score: the relevance blend decayed
// by age.
func scoreItem(it *item, queryTokens map[string]struct{}, queryVector []float32, now time.Time, halfLifeDays, recencyWeight float64) float64 {
	// Importance is a tiebreaker between relevant memories, never a signal of
	// its own: it is gated by the strongest relevance signal, so an important
	// memory about something else still scores zero.
	kw := keywordScore(queryTokens, it.tokens)
	relevance := 0.0
	if cos, ok := cosine(queryVector, it.vector); ok {
		cosPos := math.Max(0, cos)
		relevance = weightVector*cosPos + weightKeyword*kw +
			weightImportance*it.Importance*math.Max(cosPos, kw)
	} else {
		// No vector on one side or the other: its weight redistributes over
		// the keyword score, keeping importance gated the same way.
		relevance = (weightVector+weightKeyword)*kw + weightImportance*it.Importance*kw
	}

	recency := 1.0
	if halfLifeDays > 0 {
		ageDays := now.Sub(it.CreatedAt).Hours() / 24
		if ageDays > 0 {
			recency = math.Exp2(-ageDays / halfLifeDays)
		}
	}
	return (1-recencyWeight)*relevance + recencyWeight*recency*relevance
}

// keywordScore is the fraction of query tokens present in the memory's text
// and tags. It is deliberately simple: token sets are precomputed at write
// time, so a recall over thousands of memories is set lookups, not string
// scans.
func keywordScore(query, text map[string]struct{}) float64 {
	if len(query) == 0 || len(text) == 0 {
		return 0
	}
	matched := 0
	for tok := range query {
		if _, ok := text[tok]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(query))
}

// cosine returns the cosine similarity of two vectors, reporting false when
// either is missing or they disagree on dimension.
func cosine(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}

// stopwords are dropped from both sides of the keyword match: a query and a
// memory sharing only "the" have nothing in common. Deliberately not in the
// list: on, off, up, down — in home automation those are the message.
var stopwords = map[string]struct{}{}

func init() {
	for _, w := range strings.Fields(
		"the a an is are was were be been being to of in at for and or not no " +
			"do does did done why how what when where who whom which it its this " +
			"that these those with from as by about my your our their his her i " +
			"you we they he she them us me am will would can could should shall " +
			"may might must have has had if then than so very just there here") {
		stopwords[w] = struct{}{}
	}
}

// tokenize lowercases and splits on anything that is not a letter or digit,
// dropping one-character fragments and stopwords. It is the one tokenizer
// both write and query sides use, which is what makes the keyword score mean
// anything.
func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var b strings.Builder
	flush := func() {
		if b.Len() > 1 {
			if _, stop := stopwords[b.String()]; !stop {
				out[b.String()] = struct{}{}
			}
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}
