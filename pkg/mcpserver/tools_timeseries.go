package mcpserver

import (
	"context"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashchart"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type queryTimeSeriesInput struct {
	ConnectionID  string            `json:"connection_id" jsonschema:"Prometheus, InfluxDB, PostgreSQL, or Home Assistant connection UUID, or energy for energy rollups."`
	Language      string            `json:"language" jsonschema:"promql, influxql, flux (InfluxDB 2), sql (InfluxDB 3 and PostgreSQL), ha_history, ha_statistics, or energy."`
	Energy        *tsdb.EnergyQuery `json:"energy,omitempty" jsonschema:"energy: targets (house, untracked, solar, group:<id>, source:<id>), measure, and period (hour, day, week, month, or auto)."`
	Text          string            `json:"text,omitempty" jsonschema:"Query text. Macros: $__from, $__to, $__interval, $__interval_ms, $__range, $__range_s, $__rate_interval; SQL $__timeFilter(col), $__timeGroup(col, 5m); InfluxQL $timeFilter; Flux v.timeRangeStart, v.timeRangeStop, v.windowPeriod."`
	Entities      []string          `json:"entities,omitempty" jsonschema:"ha_history: 1 to 50 entity IDs."`
	Attribute     string            `json:"attribute,omitempty" jsonschema:"ha_history: read this attribute instead of the state."`
	StatisticIDs  []string          `json:"statistic_ids,omitempty" jsonschema:"ha_statistics: statistic IDs, usually sensor entity IDs."`
	Period        string            `json:"period,omitempty" jsonschema:"ha_statistics: 5minute, hour (default), day, week, or month."`
	Types         []string          `json:"types,omitempty" jsonschema:"ha_statistics: mean (default), min, max, sum, state, change."`
	From          string            `json:"from,omitempty" jsonschema:"Range start: now-6h (default), now-1d/d, RFC 3339, or Unix milliseconds."`
	To            string            `json:"to,omitempty" jsonschema:"Range end; default now."`
	MaxDataPoints int               `json:"max_data_points,omitempty" jsonschema:"Most points per series, which sets $__interval; default 1000."`
	MinInterval   string            `json:"min_interval,omitempty" jsonschema:"Lower bound for $__interval, such as 15s."`
	Format        string            `json:"format,omitempty" jsonschema:"time_series (default) or table."`
	LegendFormat  string            `json:"legend_format,omitempty" jsonschema:"Series name template such as {{instance}}."`
	Instant       bool              `json:"instant,omitempty" jsonschema:"PromQL: evaluate once at the range end."`
}

type timeSeriesMetadataInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"Readable time-series or Home Assistant connection UUID."`
	Kind         string `json:"kind" jsonschema:"metrics, labels, label_values (Prometheus); databases, measurements, tag_keys, tag_values, field_keys (InfluxDB); schemas, tables, columns (PostgreSQL); entities, statistic_ids (Home Assistant)."`
	Name         string `json:"name,omitempty" jsonschema:"Label or tag key for label_values and tag_values."`
	Match        string `json:"match,omitempty" jsonschema:"Prometheus series selector, or a substring for Home Assistant lookups."`
	Measurement  string `json:"measurement,omitempty" jsonschema:"InfluxDB measurement for tag and field lookups."`
	Schema       string `json:"schema,omitempty" jsonschema:"PostgreSQL schema for table lookups."`
	Table        string `json:"table,omitempty" jsonschema:"PostgreSQL table for column lookups."`
}

type listForwardsInput struct {
	Source string `json:"source,omitempty" jsonschema:"control:<id> or mqtt_device:<id> to return only the forward that source's editor owns."`
}

type forwardInput struct {
	ID            string            `json:"id,omitempty" jsonschema:"Forward UUID to update; omit to create."`
	Name          string            `json:"name" jsonschema:"Display name."`
	Enabled       *bool             `json:"enabled,omitempty" jsonschema:"Whether the forward records; default true."`
	SourceType    string            `json:"source_type" jsonschema:"control, mqtt_device, or ha."`
	SourceIDs     []string          `json:"source_ids,omitempty" jsonschema:"Control or MQTT device UUIDs; empty records all of that type."`
	OwnerID       string            `json:"owner_id,omitempty" jsonschema:"Control or device UUID whose editor owns this forward; source_ids becomes exactly it."`
	ConnectionID  string            `json:"connection_id,omitempty" jsonschema:"Home Assistant connection UUID for ha forwards."`
	Include       []string          `json:"include,omitempty" jsonschema:"ha: entity ID patterns with * wildcards, such as sensor.*_temperature."`
	Exclude       []string          `json:"exclude,omitempty" jsonschema:"ha: entity ID patterns left out."`
	Attributes    []string          `json:"attributes,omitempty" jsonschema:"ha: attributes recorded besides the state."`
	AllAttributes bool              `json:"all_attributes,omitempty" jsonschema:"ha: record every numeric attribute."`
	Destinations  []string          `json:"destinations" jsonschema:"1 to 10 writable connection UUIDs: prometheus, influxdb, or postgres."`
	Metric        string            `json:"metric,omitempty" jsonschema:"Metric or measurement name; default karakuri_control, karakuri_mqtt_device, or karakuri_ha_entity."`
	Labels        map[string]string `json:"labels,omitempty" jsonschema:"Extra labels on every point."`
	MinIntervalMS int64             `json:"min_interval_ms,omitempty" jsonschema:"Coalesce changes within this many milliseconds to the latest."`
}

type forwardIDInput struct {
	ID string `json:"id" jsonschema:"Forward UUID."`
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, apperrors.ErrValidation.WithMessage("invalid UUID " + s)
		}
		out = append(out, id)
	}
	return out, nil
}

// addTimeSeriesTools registers the time-series query and history forwarding
// tools.
func addTimeSeriesTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "query_timeseries",
		Description: "Run a query against a Prometheus, InfluxDB, or PostgreSQL connection, or Home Assistant's recorder history and statistics, over a time range. Returns series of typed fields; times are Unix milliseconds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in queryTimeSeriesInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		q := tsdb.Query{
			Language: in.Language, Text: in.Text, From: in.From, To: in.To, MaxDataPoints: in.MaxDataPoints,
			MinInterval: in.MinInterval, Format: in.Format, LegendFormat: in.LegendFormat, Instant: in.Instant,
			Energy: in.Energy,
		}
		if in.Language == tsdb.LangHAHistory || in.Language == tsdb.LangHAStatistics {
			q.HA = &tsdb.HAQuery{Entities: in.Entities, Attribute: in.Attribute, StatisticIDs: in.StatisticIDs, Period: in.Period, Types: in.Types}
		}
		res, err := tsdbops.Query(ctx, in.ConnectionID, q)
		if err != nil {
			return nil, nil, opErr(tsdbops.AppError(err))
		}
		return nil, res, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_timeseries_metadata",
		Description: "List metric, label, measurement, tag, table, column, entity, or statistic names of a readable time-series or Home Assistant connection.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in timeSeriesMetadataInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		values, err := tsdbops.Metadata(ctx, in.ConnectionID, tsdb.MetaRequest{
			Kind: in.Kind, Name: in.Name, Match: in.Match, Measurement: in.Measurement, Schema: in.Schema, Table: in.Table,
		})
		if err != nil {
			return nil, nil, opErr(tsdbops.AppError(err))
		}
		return nil, map[string]any{"values": values}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_history_forwards",
		Description: "List the forwards recording control, MQTT device, and Home Assistant values in time-series connections, with each destination's writer state.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listForwardsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		items, err := tsdbops.ListForwardsFor(ctx, callerID(ctx), in.Source)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"forwards": items}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_history_forward",
		Description: "Create or replace a history forward: which controls, MQTT devices, or Home Assistant entities are recorded in which writable time-series connections.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in forwardInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		f := models.HistoryForward{
			Name: in.Name, Enabled: in.Enabled == nil || *in.Enabled, SourceType: in.SourceType,
			AllAttributes: in.AllAttributes, Metric: in.Metric, MinIntervalMS: in.MinIntervalMS,
		}
		f.Include.V, f.Exclude.V, f.Attributes.V, f.Labels.V = in.Include, in.Exclude, in.Attributes, in.Labels
		var err error
		if f.SourceIDs.V, err = parseUUIDs(in.SourceIDs); err != nil {
			return nil, nil, opErr(err)
		}
		if f.Destinations.V, err = parseUUIDs(in.Destinations); err != nil {
			return nil, nil, opErr(err)
		}
		if in.OwnerID != "" {
			id, err := uuid.Parse(in.OwnerID)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid owner_id"))
			}
			f.OwnerID = &id
		}
		if in.ConnectionID != "" {
			id, err := uuid.Parse(in.ConnectionID)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection_id"))
			}
			f.ConnectionID = &id
		}
		if in.ID != "" {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid id"))
			}
			existing, err := models.FindHistoryForward(id)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("history forward not found"))
			}
			f.ID, f.CreatedAt = id, existing.CreatedAt
		}
		item, err := tsdbops.SaveForwardFor(ctx, callerID(ctx), &f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, item, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_history_forward",
		Description: "Delete a history forward. Points already written stay in the databases.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in forwardIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid id"))
		}
		if err := tsdbops.DeleteForwardFor(ctx, callerID(ctx), id); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_history_status",
		Description: "Report every writable time-series connection's history writer: state, last error and success, points buffered on disk, and written, discarded, rejected, and skipped totals.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		items, err := tsdbops.StatusesFor(ctx, callerID(ctx))
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"connections": items}, nil
	})
}

type chartDataInput struct {
	Dashboard string            `json:"dashboard" jsonschema:"Dashboard UUID or slug."`
	WidgetID  string            `json:"widget_id" jsonschema:"Chart placement UUID."`
	Vars      map[string]string `json:"vars,omitempty" jsonschema:"Variable values; each must be an option, or for a time range parse, such as now-24h,now."`
	From      string            `json:"from,omitempty" jsonschema:"Range start override, as a zoom would send."`
	To        string            `json:"to,omitempty" jsonschema:"Range end override."`
	Width     int               `json:"width,omitempty" jsonschema:"Drawing width in pixels, which sets the default maximum data points; default 1000."`
}

// addChartTools registers the chart data tool.
func addChartTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_chart_data",
		Description: "Run a dashboard chart placement's saved queries the way a viewer would, with optional variable values, and return each query's series or error. Access follows dashboard view rules; editors read the draft.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in chartDataInput) (*mcp.CallToolResult, any, error) {
		resp, err := dashchart.ForCaller(ctx, in.Dashboard, in.WidgetID, in.Vars, in.From, in.To, in.Width, nil)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, resp, nil
	})
}
