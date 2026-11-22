package tswriter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// spool is a connection's on-disk queue of batches awaiting delivery. Each
// batch is one file named <seq>-<count>.seg holding one JSON point per line,
// so appending and discarding the oldest are a file creation and a deletion,
// and the point count of a discarded file is known without reading it.
type spool struct {
	dir      string
	segments []segment
	bytes    int64
	points   int64
	next     uint64
}

type segment struct {
	seq   uint64
	count int64
	size  int64
	path  string
}

// openSpool reads an existing spool directory, creating it if needed.
// Files that do not parse as segment names are ignored; a temporary file
// left by a crash mid-write is removed.
func openSpool(dir string) (*spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s := &spool{dir: dir}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		seg, ok := parseSegment(dir, name)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		seg.size = info.Size()
		s.segments = append(s.segments, seg)
		s.bytes += seg.size
		s.points += seg.count
		if seg.seq >= s.next {
			s.next = seg.seq + 1
		}
	}
	sort.Slice(s.segments, func(i, j int) bool { return s.segments[i].seq < s.segments[j].seq })
	if len(s.segments) == 0 {
		// New sequences start high so a batch can be put back in front of
		// the oldest segment without running out of numbers.
		s.next = firstSeq
	}
	return s, nil
}

// firstSeq is the sequence an empty spool starts from.
const firstSeq = 1 << 40

func parseSegment(dir, name string) (segment, bool) {
	base, ok := strings.CutSuffix(name, ".seg")
	if !ok {
		return segment{}, false
	}
	seqText, countText, ok := strings.Cut(base, "-")
	if !ok {
		return segment{}, false
	}
	seq, err1 := strconv.ParseUint(seqText, 10, 64)
	count, err2 := strconv.ParseInt(countText, 10, 64)
	if err1 != nil || err2 != nil {
		return segment{}, false
	}
	return segment{seq: seq, count: count, path: filepath.Join(dir, name)}, true
}

func (s *spool) empty() bool { return len(s.segments) == 0 }

// append writes a batch as a new segment after the others.
func (s *spool) append(points []tsdb.Point) error {
	seq := s.next
	if err := s.write(seq, points); err != nil {
		return err
	}
	s.next++
	return nil
}

// prepend writes a batch as a new segment before the others, for points
// that were in flight when newer ones were spooled. A spool whose oldest
// segment is numbered 0 has no room in front, and appends instead.
func (s *spool) prepend(points []tsdb.Point) error {
	if s.empty() {
		return s.append(points)
	}
	first := s.segments[0].seq
	if first == 0 {
		return s.append(points)
	}
	return s.write(first-1, points)
}

// write stores a batch as segment seq and keeps the list in order. The
// file is written under a temporary name and renamed, so a crash never
// leaves a half segment.
func (s *spool) write(seq uint64, points []tsdb.Point) error {
	if len(points) == 0 {
		return nil
	}
	name := fmt.Sprintf("%020d-%d.seg", seq, len(points))
	path := filepath.Join(s.dir, name)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, p := range points {
		if err := enc.Encode(p); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	info, err := os.Stat(path)
	size := int64(0)
	if err == nil {
		size = info.Size()
	}
	s.segments = append(s.segments, segment{seq: seq, count: int64(len(points)), size: size, path: path})
	sort.Slice(s.segments, func(i, j int) bool { return s.segments[i].seq < s.segments[j].seq })
	s.bytes += size
	s.points += int64(len(points))
	return nil
}

// oldest reads the first segment's points.
func (s *spool) oldest() ([]tsdb.Point, error) {
	if s.empty() {
		return nil, nil
	}
	f, err := os.Open(s.segments[0].path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var points []tsdb.Point
	dec := json.NewDecoder(bufio.NewReader(f))
	for dec.More() {
		var p tsdb.Point
		if err := dec.Decode(&p); err != nil {
			// A segment that does not decode cannot be delivered; the caller
			// drops it rather than retrying it forever.
			return points, err
		}
		points = append(points, p)
	}
	return points, nil
}

// dropOldest deletes the first segment and returns its point count.
func (s *spool) dropOldest() int64 {
	if s.empty() {
		return 0
	}
	seg := s.segments[0]
	_ = os.Remove(seg.path)
	s.segments = s.segments[1:]
	s.bytes -= seg.size
	s.points -= seg.count
	return seg.count
}

// dropSeq deletes segment seq when it is still the oldest. A trim may have
// discarded it while it was being delivered.
func (s *spool) dropSeq(seq uint64) {
	if !s.empty() && s.segments[0].seq == seq {
		s.dropOldest()
	}
}

// trim discards the oldest segments until the spool fits max bytes, and
// returns the number of points discarded.
func (s *spool) trim(max int64) int64 {
	var dropped int64
	for s.bytes > max && len(s.segments) > 0 {
		dropped += s.dropOldest()
	}
	return dropped
}

// remove deletes the spool directory and everything in it.
func (s *spool) remove() error {
	s.segments = nil
	s.bytes, s.points = 0, 0
	return os.RemoveAll(s.dir)
}
