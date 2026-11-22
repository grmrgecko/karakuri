package mcpserver

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/energyops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type energyIDInput struct {
	ID string `json:"id" jsonschema:"UUID of the source, group, or tariff."`
}

type saveEnergySourceInput struct {
	ID string `json:"id,omitempty" jsonschema:"Source UUID to update; omit to create."`
	energyops.SourceInput
}

type saveEnergyGroupInput struct {
	ID string `json:"id,omitempty" jsonschema:"Group UUID to update; omit to create."`
	energyops.GroupInput
}

type saveEnergyTariffInput struct {
	ID            string                 `json:"id,omitempty" jsonschema:"Tariff UUID to update; omit to create."`
	Name          string                 `json:"name" jsonschema:"Display name."`
	Currency      string                 `json:"currency" jsonschema:"ISO 4217 code such as USD."`
	EffectiveFrom string                 `json:"effective_from" jsonschema:"Local date YYYY-MM-DD the tariff starts."`
	Import        models.EnergyRateSpec  `json:"import" jsonschema:"Import rate: {flat}, {periods:[{name,days,start,end,rate}]} covering the whole week, or {control_id}."`
	Export        *models.EnergyRateSpec `json:"export,omitempty" jsonschema:"Export credit in the same shapes, optional."`
	DailyCharge   float64                `json:"daily_charge,omitempty" jsonschema:"Fixed charge per day."`
}

type importEnergyInput struct {
	ID   string `json:"id" jsonschema:"Source UUID."`
	Days int    `json:"days" jsonschema:"Days of history to read, 1 to 400."`
}

type energyPriceInput struct {
	At string `json:"at,omitempty" jsonschema:"RFC 3339 time; omit for now."`
}

type energyCheapestInput struct {
	Hours float64 `json:"hours" jsonschema:"Window length in hours, 0.25 to 48."`
	From  string  `json:"from,omitempty" jsonschema:"Earliest start: now, now/d, or RFC 3339. Default now."`
	To    string  `json:"to,omitempty" jsonschema:"Latest end. Default now+24h."`
}

func parseOptionalID(raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("invalid id")
	}
	return id, nil
}

// addEnergyTools registers the energy tools.
func addEnergyTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_energy_sources",
		Description: "List energy sources with role, what each reads, state, last reading, groups, and today's usage in kWh."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			list, err := energyops.ListSources()
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"sources": list}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_energy_candidates",
		Description: "List devices, controls, and Home Assistant entities reporting energy (kWh) or power (W), each with the source fields that would read it."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			list, err := energyops.CandidatesFor(ctx, callerID(ctx))
			if err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			return nil, map[string]any{"candidates": list}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "save_energy_source",
		Description: "Create or update an energy source: what it reads (source_kind control, device, or ha with source_id, entity_id, attribute), measurement energy or power, unit, and role consumer, house, import, export, or production."},
		func(ctx context.Context, req *mcp.CallToolRequest, in saveEnergySourceInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			src, err := energyops.SaveSourceFor(ctx, callerID(ctx), id, in.SourceInput)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, src, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_energy_source",
		Description: "Delete an energy source. Its recorded usage is removed 30 days later."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyIDInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil || id == uuid.Nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("id is required"))
			}
			if err := energyops.DeleteSource(id); err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"deleted": true}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "import_energy_history",
		Description: "Read up to 400 days of a source's history in the background, from Home Assistant statistics or the destination its history forward writes to, filling hours with no live figure. list_energy_sources shows the progress."},
		func(ctx context.Context, req *mcp.CallToolRequest, in importEnergyInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil || id == uuid.Nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("id is required"))
			}
			if err := energyops.ImportFor(ctx, callerID(ctx), id, in.Days); err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"started": true}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_energy_groups", Description: "List energy groups with their current sources."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			list, err := energyops.ListGroups()
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"groups": list}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "save_energy_group",
		Description: "Create or update an energy group such as a room or circuit, with its source_ids. Membership changes apply from now on."},
		func(ctx context.Context, req *mcp.CallToolRequest, in saveEnergyGroupInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			g, err := energyops.SaveGroup(id, in.GroupInput)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, g, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_energy_group", Description: "Delete an energy group; its sources stay."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyIDInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil || id == uuid.Nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("id is required"))
			}
			if err := energyops.DeleteGroup(id); err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"deleted": true}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_energy_tariffs", Description: "List the house's tariffs by effective date."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			list, err := energyops.ListTariffs()
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"tariffs": list}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "save_energy_tariff",
		Description: "Create or update a tariff and reprice the recorded hours it covers. Periods use ISO weekdays 1 to 7 and local HH:MM; together they must cover the whole week without overlap."},
		func(ctx context.Context, req *mcp.CallToolRequest, in saveEnergyTariffInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			t := models.EnergyTariff{Name: in.Name, Currency: in.Currency, EffectiveFrom: in.EffectiveFrom, DailyCharge: in.DailyCharge}
			t.Import.V = in.Import
			t.Export.V = in.Export
			res, err := energyops.SaveTariff(id, t)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, res, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_energy_tariff", Description: "Delete a tariff and reprice its hours."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyIDInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
				return nil, nil, err
			}
			id, err := parseOptionalID(in.ID)
			if err != nil || id == uuid.Nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("id is required"))
			}
			n, err := energyops.DeleteTariff(id)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"deleted": true, "hours_recomputed": n}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_energy_rollups",
		Description: "Get a target's figures per hour, day, week, or month. target house, untracked, solar, group:<id>, or source:<id>; measures usage, cost, export_credit, daily_charge, total_cost, import, export, production, self_consumption, solar_share."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyops.RollupQuery) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			r, err := energyops.Rollups(in)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, r, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_energy_price",
		Description: "Get the import price, export credit, and time-of-use period at a time, and when the price next changes."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyPriceInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			var at time.Time
			if in.At != "" {
				var err error
				if at, err = time.Parse(time.RFC3339, in.At); err != nil {
					return nil, nil, opErr(apperrors.ErrValidation.WithMessage("at must be an RFC 3339 time"))
				}
			}
			p, err := energyops.Price(at)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, p, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "create_energy_dashboard",
		Description: "Create a dashboard of energy charts for the caller: today, this week, this month, 30 days of use, a breakdown by group and source with untracked use, and solar against the grid when there are solar sources."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermDashboardsWrite); err != nil {
				return nil, nil, err
			}
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			d, err := energyops.CreatePreset(ctx, uid)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"id": d.ID, "slug": d.Slug, "name": d.Name}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "find_cheapest_energy_window",
		Description: "Find the cheapest window of the given hours within a range under the tariff, starting on a quarter hour."},
		func(ctx context.Context, req *mcp.CallToolRequest, in energyCheapestInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
				return nil, nil, err
			}
			if in.Hours < 0.25 || in.Hours > 48 {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("hours must be 0.25 to 48"))
			}
			w, err := energyops.Cheapest(time.Duration(in.Hours*float64(time.Hour)), in.From, in.To)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, w, nil
		})
}
