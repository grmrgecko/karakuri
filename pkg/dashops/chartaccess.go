package dashops

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
)

// chartPackage is the chart widget, whose placements hold database queries.
const chartPackage = "chart"

// chartQueries returns what a chart placement asks of its databases: each
// query's connection, language, text, and Home Assistant and energy
// selections, in a stable form. Display options are left out, so restyling a
// chart is not a change of what it reads.
func chartQueries(config string) string {
	var c struct {
		Queries []struct {
			ConnectionID string          `json:"connectionId"`
			Language     string          `json:"language"`
			Text         string          `json:"text"`
			HA           json.RawMessage `json:"ha"`
			Energy       json.RawMessage `json:"energy"`
		} `json:"queries"`
	}
	if config == "" || json.Unmarshal([]byte(config), &c) != nil {
		return ""
	}
	// A query naming no connection, text, or selection reads nothing, as
	// the editor's blank first query does.
	blank := func(raw json.RawMessage) bool {
		s := string(raw)
		return s == "" || s == "null" || s == "{}"
	}
	kept := c.Queries[:0]
	for _, q := range c.Queries {
		if q.ConnectionID == "" && q.Text == "" && blank(q.HA) && blank(q.Energy) {
			continue
		}
		kept = append(kept, q)
	}
	if len(kept) == 0 {
		return ""
	}
	out, _ := json.Marshal(kept)
	return string(out)
}

// checkChartQueries refuses a chart placement whose queries change unless
// the user may read connections. A dashboard editor without that
// permission could otherwise save a query against any connection and read
// its results through the chart; the editor's preview already needs it.
// Moving, resizing, or restyling a chart changes no query and needs
// nothing. A zero user is the server itself.
func checkChartQueries(ctx context.Context, userID uuid.UUID, pkg, oldConfig, newConfig string) error {
	if pkg != chartPackage || userID == uuid.Nil {
		return nil
	}
	next := chartQueries(newConfig)
	if next == chartQueries(oldConfig) || next == "" {
		return nil
	}
	if !auth.Can(ctx, userID, perm.PermConnectionsRead) {
		return apperrors.ErrForbidden.WithMessage("changing a chart's queries needs permission to read connections (" + perm.PermConnectionsRead + ")")
	}
	return checkConnectionIDs(ctx, userID, chartConnectionIDs(newConfig))
}

// CheckVariableQueries refuses time-series query variables in next that are
// not already in prev unless the user may read connections. Their query text
// runs against a database and the results become options served to every
// viewer, so they read a connection the way a chart query does. Home
// Assistant queries are not covered: they list entity IDs within the grant
// described in docs/dashboard-security.md. A zero user is the server itself.
func CheckVariableQueries(ctx context.Context, userID uuid.UUID, prev, next *models.DashboardDisplay) error {
	if userID == uuid.Nil {
		return nil
	}
	kept := map[string]bool{}
	for _, q := range timeSeriesVariableQueries(prev) {
		kept[q] = true
	}
	for _, q := range timeSeriesVariableQueries(next) {
		if kept[q] {
			continue
		}
		if !auth.Can(ctx, userID, perm.PermConnectionsRead) {
			return apperrors.ErrForbidden.WithMessage("changing a time-series variable query needs permission to read connections (" + perm.PermConnectionsRead + ")")
		}
		var query models.VariableQuery
		if json.Unmarshal([]byte(q), &query) == nil {
			if err := checkConnectionIDs(ctx, userID, []string{query.ConnectionID}); err != nil {
				return err
			}
		}
	}
	return nil
}

// timeSeriesVariableQueries returns each time-series query variable's query
// in a stable form.
func timeSeriesVariableQueries(d *models.DashboardDisplay) []string {
	if d == nil {
		return nil
	}
	var out []string
	for _, v := range d.Variables {
		if v.Kind() != models.VariableTypeQuery || v.Query == nil || !dashvars.IsTimeSeriesSource(v.Query.Source) {
			continue
		}
		b, _ := json.Marshal(v.Query)
		out = append(out, string(b))
	}
	return out
}
