package tsdbops

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tswriter"
	"gorm.io/gorm"
)

// Forward bounds from the data model.
const (
	maxDestinations = 10
	maxLabels       = 20
	maxMinInterval  = 86_400_000
)

// reservedLabels are set by the forwarder itself.
var reservedLabels = map[string]bool{
	"id": true, "name": true, "attribute": true, "unit": true, "entity_id": true,
	"domain": true, "friendly_name": true, "connection": true, "state": true,
}

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var metricName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// ForwardItem is a forward with each destination's writer state.
type ForwardItem struct {
	models.HistoryForward
	Status []DestinationStatus `json:"status" description:"Writer state of each destination."`
}

// DestinationStatus is one destination of a forward.
type DestinationStatus struct {
	ConnectionID uuid.UUID        `json:"connection_id" description:"Destination connection."`
	Name         string           `json:"name,omitempty" description:"Connection name."`
	Missing      bool             `json:"missing,omitempty" description:"Whether the connection no longer exists or cannot be written."`
	Writer       *tswriter.Status `json:"writer,omitempty" description:"Writer state, once it has handled points."`
}

// Reloader is told when forwards change. The forwarder sets it at startup.
var Reloader func()

func reload() {
	if Reloader != nil {
		Reloader()
	}
}

// ListForwards returns every forward, or with source set ("control:<id>" or
// "mqtt_device:<id>") only the forward that source's editor owns.
func ListForwards(source string) ([]ForwardItem, error) {
	var forwards []*models.HistoryForward
	if source != "" {
		_, idText, ok := strings.Cut(source, ":")
		id, err := uuid.Parse(idText)
		if !ok || err != nil {
			return nil, apperrors.ErrValidation.WithMessage("source must be control:<id> or mqtt_device:<id>")
		}
		f, err := models.FindOwnedHistoryForward(id)
		if err == nil {
			forwards = append(forwards, f)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.ErrInternal.WithError(err)
		}
	} else {
		var err error
		if forwards, err = models.ListHistoryForwards(); err != nil {
			return nil, apperrors.ErrInternal.WithError(err)
		}
	}
	out := make([]ForwardItem, 0, len(forwards))
	for _, f := range forwards {
		out = append(out, item(f))
	}
	return out, nil
}

func item(f *models.HistoryForward) ForwardItem {
	it := ForwardItem{HistoryForward: *f, Status: []DestinationStatus{}}
	for _, id := range f.Destinations.V {
		st := DestinationStatus{ConnectionID: id}
		c, err := models.FindConnectionByID(id)
		if err != nil || !tsdb.IsWritable(c.Kind) {
			st.Missing = true
		} else {
			st.Name = c.Name
		}
		if tswriter.R != nil {
			st.Writer = tswriter.R.Status(id)
		}
		it.Status = append(it.Status, st)
	}
	return it
}

// SaveForward validates and stores a forward, creating it when its ID is
// zero, and reloads the forwarder.
func SaveForward(f *models.HistoryForward) (ForwardItem, error) {
	if err := validateForward(f); err != nil {
		return ForwardItem{}, err
	}
	if f.ID != uuid.Nil {
		if _, err := models.FindHistoryForward(f.ID); err != nil {
			return ForwardItem{}, apperrors.ErrNotFound.WithMessage("history forward not found")
		}
	}
	if f.OwnerID != nil {
		if existing, err := models.FindOwnedHistoryForward(*f.OwnerID); err == nil && existing.ID != f.ID {
			if f.ID != uuid.Nil {
				return ForwardItem{}, apperrors.ErrConflict.WithMessage("that source already has a forward of its own")
			}
			f.ID = existing.ID
			f.CreatedAt = existing.CreatedAt
		}
	}
	// The enabled column defaults to true, and an insert leaves a false to
	// that default, so a forward first saved disabled is switched off in the
	// same transaction rather than stored enabled.
	enabled := f.Enabled
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(f).Error; err != nil {
			return err
		}
		if enabled {
			return nil
		}
		f.Enabled = false
		return tx.Model(f).UpdateColumn("enabled", false).Error
	})
	if err != nil {
		return ForwardItem{}, apperrors.ErrInternal.WithError(err)
	}
	reload()
	return item(f), nil
}

// DeleteForward removes a forward and reloads the forwarder.
func DeleteForward(id uuid.UUID) error {
	res := db.DB.Delete(&models.HistoryForward{}, "id = ?", id)
	if res.Error != nil {
		return apperrors.ErrInternal.WithError(res.Error)
	}
	if res.RowsAffected == 0 {
		return apperrors.ErrNotFound.WithMessage("history forward not found")
	}
	reload()
	return nil
}

// DeleteOwned removes the forward a control or device owned, when it is
// deleted.
func DeleteOwned(owner uuid.UUID) {
	if res := db.DB.Delete(&models.HistoryForward{}, "owner_id = ?", owner); res.Error == nil && res.RowsAffected > 0 {
		reload()
	}
}

func validateForward(f *models.HistoryForward) error {
	bad := func(msg string) error { return apperrors.ErrValidation.WithMessage(msg) }
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" || len(f.Name) > 120 {
		return bad("name must be 1 to 120 bytes")
	}
	switch f.SourceType {
	case models.ForwardSourceControl, models.ForwardSourceMQTTDevice:
		f.ConnectionID = nil
		f.Include.V, f.Exclude.V, f.Attributes.V, f.AllAttributes = nil, nil, nil, false
		if f.OwnerID != nil && (len(f.SourceIDs.V) != 1 || f.SourceIDs.V[0] != *f.OwnerID) {
			f.SourceIDs.V = []uuid.UUID{*f.OwnerID}
		}
	case models.ForwardSourceHA:
		f.SourceIDs.V, f.OwnerID = nil, nil
		if f.ConnectionID == nil {
			return bad("a Home Assistant forward needs its connection")
		}
		c, err := models.FindConnectionByID(*f.ConnectionID)
		if err != nil || c.Kind != homeassistant.Kind {
			return bad("connection_id must name a Home Assistant connection")
		}
		if len(cleanList(f.Include.V)) == 0 {
			return bad("a Home Assistant forward needs at least one include pattern")
		}
		f.Include.V, f.Exclude.V, f.Attributes.V = cleanList(f.Include.V), cleanList(f.Exclude.V), cleanList(f.Attributes.V)
	default:
		return bad("source_type must be control, mqtt_device, or ha")
	}
	if len(f.Destinations.V) == 0 || len(f.Destinations.V) > maxDestinations {
		return bad(fmt.Sprintf("a forward needs 1 to %d destinations", maxDestinations))
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range f.Destinations.V {
		if seen[id] {
			return bad("destinations must not repeat")
		}
		seen[id] = true
		c, err := models.FindConnectionByID(id)
		if err != nil || !tsdb.IsWritable(c.Kind) {
			return bad(fmt.Sprintf("destination %s is not a writable time-series connection", id))
		}
	}
	f.Metric = strings.TrimSpace(f.Metric)
	if f.Metric != "" && !metricName.MatchString(f.Metric) {
		return bad("metric must be letters, digits, underscores, or colons, not starting with a digit")
	}
	if len(f.Labels.V) > maxLabels {
		return bad(fmt.Sprintf("a forward has at most %d labels", maxLabels))
	}
	for k := range f.Labels.V {
		if !labelName.MatchString(k) {
			return bad(fmt.Sprintf("label %q must be letters, digits, or underscores, not starting with a digit", k))
		}
		if reservedLabels[k] {
			return bad(fmt.Sprintf("label %q is set by the forwarder", k))
		}
	}
	if f.MinIntervalMS < 0 || f.MinIntervalMS > maxMinInterval {
		return bad("min_interval_ms must be 0 to 86400000")
	}
	return nil
}

func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// WriterStatus is one writable connection's writer state.
type WriterStatus struct {
	ConnectionID uuid.UUID        `json:"connection_id" description:"Connection ID."`
	Name         string           `json:"name" description:"Connection name."`
	Kind         string           `json:"kind" description:"Connection kind."`
	Writer       *tswriter.Status `json:"writer,omitempty" description:"Writer state; absent before the writer has handled points."`
}

// Statuses lists every writable connection's writer state.
func Statuses() ([]WriterStatus, error) {
	conns, err := models.ListConnections()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	out := []WriterStatus{}
	for _, c := range conns {
		if !tsdb.IsWritable(c.Kind) {
			continue
		}
		st := WriterStatus{ConnectionID: c.ID, Name: c.Name, Kind: c.Kind}
		if tswriter.R != nil {
			st.Writer = tswriter.R.Status(c.ID)
		}
		out = append(out, st)
	}
	return out, nil
}

// ResolveSink finds a connection's running sink for the writer. A disabled
// connection, or one with writing off, is tswriter.ErrDisabled.
func ResolveSink(id uuid.UUID) (tsdb.Sink, error) {
	c, err := models.FindConnectionByID(id)
	if err != nil {
		return nil, tswriter.ErrDisabled
	}
	if !c.Enabled {
		return nil, tswriter.ErrDisabled
	}
	mgr := connops.Manager()
	if mgr == nil {
		return nil, fmt.Errorf("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		if st, ok := mgr.Status(c.ID); ok && st.State == integrations.StateError && st.Error != "" {
			return nil, fmt.Errorf("connection is not running: %s", st.Error)
		}
		return nil, fmt.Errorf("connection is not running")
	}
	sink, ok := d.(tsdb.Sink)
	if !ok {
		return nil, tswriter.ErrDisabled
	}
	if !sink.SinkInfo().Writable {
		return nil, tswriter.ErrDisabled
	}
	return sink, nil
}

// Writer returns a writable connection's writer, for the write node.
func Writer(connID string) (*tswriter.Writer, tsdb.SinkInfo, error) {
	c, err := connops.Load(connID)
	if err != nil {
		return nil, tsdb.SinkInfo{}, err
	}
	if !tsdb.IsWritable(c.Kind) {
		return nil, tsdb.SinkInfo{}, apperrors.ErrValidation.WithMessage("connection is not a writable time-series connection")
	}
	if tswriter.R == nil {
		return nil, tsdb.SinkInfo{}, apperrors.ErrServiceUnavailable.WithMessage("history writers are not running")
	}
	w, err := tswriter.R.For(c.ID)
	if err != nil {
		return nil, tsdb.SinkInfo{}, apperrors.ErrInternal.WithError(err)
	}
	var info tsdb.SinkInfo
	if mgr := connops.Manager(); mgr != nil {
		if d, ok := mgr.Get(c.ID); ok {
			if s, ok := d.(tsdb.Sink); ok {
				info = s.SinkInfo()
			}
		}
	}
	return w, info, nil
}
