package energy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"go.uber.org/zap"
)

// Fill limits.
const (
	// MaxImportDays bounds a history import.
	MaxImportDays = 400
	// chunkPoints bounds the readings one day's request returns.
	chunkPoints = 1440
	// fillChunk is how much history one request reads.
	fillChunk = 24 * time.Hour
	// lead is how far beyond a window readings are read: before it, so the
	// first hour has a reading to measure from, and after it, so the last
	// hour gets its share of the reading that closes it. A history import
	// reads a day at a time from an unaligned start, and the hour that
	// holds a chunk's end is kept as filled by the next chunk, so it has to
	// be whole when the first chunk writes it.
	lead = 2 * time.Hour
)

// QueryHistory runs a time-series query; the server sets it to the
// time-series service so this package need not import it.
var QueryHistory func(ctx context.Context, connID string, q tsdb.Query) (*tsdb.Result, error)

// sample is one historical reading.
type sample struct {
	at    time.Time
	value float64
}

// ErrNoHistory is a source with no history karakuri knows how to read.
var ErrNoHistory = errors.New("no history to read: forward the source to a Prometheus or InfluxDB connection, or read a Home Assistant entity's state")

// Fill recomputes the hours in [from, to) that were missed or carried over
// a gap from the source's history, and returns how many hours it wrote.
// Hours recorded live are left alone, and the last two hours are the
// recorder's.
func Fill(ctx context.Context, src *models.EnergySource, from, to time.Time) (int, error) {
	loc := Location()
	limit := HourStart(time.Now(), loc).Add(-time.Hour)
	if to.After(limit) {
		to = limit
	}
	if !to.After(from) {
		return 0, nil
	}
	from = HourStart(from, loc)
	existing, err := models.EnergyHoursBetween([]uuid.UUID{src.ID}, from, to)
	if err != nil {
		return 0, err
	}
	keep := map[time.Time]bool{}
	for _, h := range existing {
		if h.Quality == models.EnergyQualityLive || h.Quality == models.EnergyQualityFilled {
			keep[h.Hour.UTC()] = true
		}
	}
	totals := map[time.Time]float64{}
	if src.SourceKind == models.EnergyKindHA {
		if !hasHistory(src) {
			return 0, ErrNoHistory
		}
		if err := haStatistics(ctx, src, from, to, loc, totals); err != nil {
			return 0, err
		}
	} else {
		samples, err := forwardedSamples(ctx, src, from.Add(-lead), to.Add(lead))
		if err != nil {
			return 0, err
		}
		cfg := ConfigOf(src)
		st := MeterState{}
		now := time.Now()
		for _, s := range samples {
			var cs []Contribution
			st, cs = Apply(cfg, st, s.value, s.at, now, loc, true)
			for _, c := range cs {
				totals[c.Hour.UTC()] += c.KWh
			}
		}
	}
	var rows []models.EnergyHour
	for h, kwh := range totals {
		if h.Before(from) || !h.Before(to) || keep[h] {
			continue
		}
		rows = append(rows, models.EnergyHour{SourceID: src.ID, Hour: h, LocalDate: LocalDate(h, loc), KWh: kwh, Quality: models.EnergyQualityFilled})
	}
	if err := models.UpsertEnergyHours(db.DB, rows); err != nil {
		return 0, err
	}
	if len(rows) > 0 {
		if _, err := RecomputeRates(from, to); err != nil {
			return len(rows), err
		}
	}
	return len(rows), nil
}

// haStatistics reads an entity's hourly statistics: the change of an energy
// meter, or the mean of a power sensor over the hour. Home Assistant's hours
// start on the UTC hour, so each is shared between the local hours it
// overlaps: two of them in a zone offset by 30 or 45 minutes.
func haStatistics(ctx context.Context, src *models.EnergySource, from, to time.Time, loc *time.Location, totals map[time.Time]float64) error {
	driver, err := connops.HADriver(src.SourceID)
	if err != nil {
		return fmt.Errorf("reading Home Assistant statistics: %w", err)
	}
	factor, err := UnitFactor(src.Measurement, src.Unit)
	if err != nil {
		return err
	}
	stat := "change"
	if src.Measurement == models.EnergyMeasurePower {
		stat = "mean"
	}
	rows, err := driver.Statistics(ctx, []string{src.EntityID}, "hour", []string{stat}, from.Add(-time.Hour), to.Add(time.Hour))
	if err != nil {
		return fmt.Errorf("reading Home Assistant statistics: %w", err)
	}
	for _, r := range rows[src.EntityID] {
		v := r.Change
		if stat == "mean" {
			v = r.Mean
		}
		if v == nil || *v < 0 {
			continue
		}
		// A power mean over one hour is that many kWh once in kW.
		start := time.UnixMilli(r.Start)
		for _, c := range split(start, start.Add(time.Hour), *v*factor, loc) {
			totals[c.Hour.UTC()] += c.KWh
		}
	}
	return nil
}

// forwardedSamples reads a control's or device's readings back from a
// destination its history forward writes to, in day chunks.
func forwardedSamples(ctx context.Context, src *models.EnergySource, from, to time.Time) ([]sample, error) {
	if QueryHistory == nil {
		return nil, ErrNoHistory
	}
	dest, metric, err := forwardFor(src)
	if err != nil {
		return nil, err
	}
	conn, err := connops.Load(dest.String())
	if err != nil {
		return nil, err
	}
	attr := src.Attribute
	if src.SourceKind == models.EnergyKindDevice && attr == "" {
		attr = "value"
	}
	var lang, text string
	switch conn.Kind {
	case "prometheus":
		sel := fmt.Sprintf(`%s{id=%q`, metric, strings.ToLower(src.SourceID))
		if attr != "" {
			sel += fmt.Sprintf(`,attribute=%q`, attr)
		}
		lang, text = tsdb.LangPromQL, sel+"}"
	case "influxdb":
		where := fmt.Sprintf(`"id" = '%s'`, strings.ToLower(src.SourceID))
		if attr != "" {
			where += fmt.Sprintf(` AND "attribute" = '%s'`, strings.ReplaceAll(attr, "'", ""))
		}
		lang, text = tsdb.LangInfluxQL, fmt.Sprintf(`SELECT "value" FROM "%s" WHERE %s AND $timeFilter`, metric, where)
	default:
		return nil, ErrNoHistory
	}
	var out []sample
	for start := from; start.Before(to); start = start.Add(fillChunk) {
		end := start.Add(fillChunk)
		if end.After(to) {
			end = to
		}
		res, err := QueryHistory(ctx, dest.String(), tsdb.Query{Language: lang, Text: text,
			From: start.UTC().Format(time.RFC3339), To: end.UTC().Format(time.RFC3339), MaxDataPoints: chunkPoints})
		if err != nil {
			return nil, fmt.Errorf("reading %s history: %w", conn.Name, err)
		}
		for _, s := range res.Series {
			if len(s.Fields) < 2 {
				continue
			}
			for i := range s.Fields[0].Values {
				ms, ok := toNumber(s.Fields[0].Values[i])
				if !ok {
					continue
				}
				v, ok := toNumber(s.Fields[1].Values[i])
				if !ok {
					continue
				}
				out = append(out, sample{at: time.UnixMilli(int64(ms)), value: v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out, nil
}

// forwardFor finds a readable destination a forward writes the source to,
// and the metric it uses.
func forwardFor(src *models.EnergySource) (uuid.UUID, string, error) {
	id, err := uuid.Parse(src.SourceID)
	if err != nil {
		return uuid.Nil, "", ErrNoHistory
	}
	forwards, err := models.ListHistoryForwards()
	if err != nil {
		return uuid.Nil, "", err
	}
	want, metric := models.ForwardSourceControl, "karakuri_control"
	if src.SourceKind == models.EnergyKindDevice {
		want, metric = models.ForwardSourceMQTTDevice, "karakuri_mqtt_device"
	}
	for _, f := range forwards {
		if !f.Enabled || f.SourceType != want {
			continue
		}
		selected := len(f.SourceIDs.V) == 0
		for _, s := range f.SourceIDs.V {
			if s == id {
				selected = true
			}
		}
		if !selected {
			continue
		}
		for _, d := range f.Destinations.V {
			c, err := connops.Load(d.String())
			if err != nil || (c.Kind != "prometheus" && c.Kind != "influxdb") {
				continue
			}
			if f.Metric != "" {
				metric = f.Metric
			}
			return d, metric, nil
		}
	}
	return uuid.Nil, "", ErrNoHistory
}

// ImportProgress is a running or finished history import.
type ImportProgress struct {
	Running bool      `json:"running" description:"Whether the import is still reading."`
	Days    int       `json:"days" description:"Days asked for."`
	Done    int       `json:"done" description:"Days read so far."`
	Hours   int       `json:"hours" description:"Hours written."`
	Error   string    `json:"error,omitempty" description:"Why the import stopped."`
	At      time.Time `json:"at" description:"When the import last moved."`
}

var (
	importMu sync.Mutex
	imports  = map[uuid.UUID]*ImportProgress{}
)

// Imports returns the progress of every import since the server started.
func Imports() map[uuid.UUID]ImportProgress {
	importMu.Lock()
	defer importMu.Unlock()
	out := make(map[uuid.UUID]ImportProgress, len(imports))
	for id, p := range imports {
		out[id] = *p
	}
	return out
}

// StartImport reads up to days of a source's history in the background,
// a day at a time, filling hours with no live figure.
func StartImport(src *models.EnergySource, days int, log *zap.Logger) error {
	if days < 1 || days > MaxImportDays {
		return fmt.Errorf("days must be 1 to %d", MaxImportDays)
	}
	if src.SourceKind == models.EnergyKindHA {
		if !hasHistory(src) {
			return ErrNoHistory
		}
	} else if _, _, err := forwardFor(src); err != nil {
		return err
	}
	importMu.Lock()
	if p := imports[src.ID]; p != nil && p.Running {
		importMu.Unlock()
		return errors.New("an import of this source is already running")
	}
	p := &ImportProgress{Running: true, Days: days, At: time.Now()}
	imports[src.ID] = p
	importMu.Unlock()
	go func() {
		ctx := context.Background()
		end := time.Now()
		for d := days; d > 0; d-- {
			from := end.Add(-time.Duration(d) * 24 * time.Hour)
			n, err := Fill(ctx, src, from, from.Add(24*time.Hour))
			importMu.Lock()
			p.Done++
			p.Hours += n
			p.At = time.Now()
			if err != nil {
				p.Error, p.Running = err.Error(), false
				importMu.Unlock()
				if log != nil {
					log.Warn("energy history import stopped", zap.String("source", src.Name), zap.Error(err))
				}
				return
			}
			importMu.Unlock()
		}
		importMu.Lock()
		p.Running = false
		importMu.Unlock()
		if log != nil {
			log.Info("energy history imported", zap.String("source", src.Name), zap.Int("hours", p.Hours))
		}
	}()
	return nil
}

// gapWindow is a stretch a source was silent across, waiting until its
// hours are old enough to fill from history.
type gapWindow struct {
	source uuid.UUID
	from   time.Time
	to     time.Time
}

// hasHistory reports whether a source's missed hours can be filled. Home
// Assistant keeps statistics for an entity's state, not its attributes.
func hasHistory(s *models.EnergySource) bool {
	if s.SourceKind == models.EnergyKindHA {
		return s.Attribute == ""
	}
	_, _, err := forwardFor(s)
	return err == nil
}

// fillDue fills the gap windows whose hours the recorder no longer owns, in
// the background, and keeps the rest.
func (r *Recorder) fillDue(now time.Time) {
	limit := HourStart(now, Location()).Add(-time.Hour)
	var due, later []gapWindow
	for _, g := range r.gaps {
		if g.to.Before(limit) {
			due = append(due, g)
		} else {
			later = append(later, g)
		}
	}
	r.gaps = later
	for _, g := range due {
		l, ok := r.sources[g.source]
		if !ok {
			continue
		}
		src := *l.model
		go func(g gapWindow) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			n, err := Fill(ctx, &src, g.from, g.to.Add(time.Hour))
			if err != nil {
				r.log.Warn("could not fill missed energy hours", zap.String("source", src.Name), zap.Error(err))
				return
			}
			r.log.Info("filled missed energy hours", zap.String("source", src.Name), zap.Int("hours", n))
		}(g)
	}
}
