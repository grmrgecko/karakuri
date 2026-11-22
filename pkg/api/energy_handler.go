package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/energyops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// energyID reads a path UUID.
func energyID(raw, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("invalid " + what + " id")
	}
	return id, nil
}

// EnergyIDInput names one energy object.
type EnergyIDInput struct {
	ID string `path:"id" description:"Object UUID."`
}

// EnergySourceSaveInput is a source to save.
type EnergySourceSaveInput struct {
	ID string `path:"id" json:"-" description:"Source UUID."`
	energyops.SourceInput
}

// EnergyGroupSaveInput is a group to save.
type EnergyGroupSaveInput struct {
	ID string `path:"id" json:"-" description:"Group UUID."`
	energyops.GroupInput
}

// EnergyTariffSaveInput is a tariff to save.
type EnergyTariffSaveInput struct {
	ID string `path:"id" json:"-" description:"Tariff UUID."`
	models.EnergyTariff
}

// EnergyPriceInput asks for the price at a time.
type EnergyPriceInput struct {
	At string `query:"at" description:"RFC 3339 time; empty is now."`
}

// EnergyCheapestInput asks for the cheapest window.
type EnergyCheapestInput struct {
	Hours float64 `query:"hours" required:"true" minimum:"0.25" maximum:"48" description:"Window length in hours."`
	From  string  `query:"from" description:"Earliest start: now, now/d, or RFC 3339. Default now."`
	To    string  `query:"to" description:"Latest end. Default now+24h."`
}

func energyListSources() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]energyops.SourceView]) error {
		list, err := energyops.ListSources()
		out.Data = list
		return err
	})
	u.SetTitle("List Energy Sources")
	u.SetDescription("Lists energy sources with their state, last reading, groups, and today's usage.")
	u.SetTags("Energy")
	return u
}

func energyCandidates() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]energy.Candidate]) error {
		list, err := energyops.CandidatesFor(ctx, callerID(ctx))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		if list == nil {
			list = []energy.Candidate{}
		}
		out.Data = list
		return nil
	})
	u.SetTitle("List Energy Candidates")
	u.SetDescription("Lists devices, controls, and Home Assistant entities that report energy or power, with the source each would make.")
	u.SetTags("Energy")
	return u
}

func energySaveSource(create bool) usecase.Interactor {
	if create {
		u := usecase.NewInteractor(func(ctx context.Context, in energyops.SourceInput, out *apiutil.Success[*models.EnergySource]) error {
			s, err := energyops.SaveSourceFor(ctx, callerID(ctx), uuid.Nil, in)
			out.Data = s
			return err
		})
		return energyDoc(&u, "Create Energy Source", "Saves an energy source reading a control, device, or Home Assistant entity.")
	}
	u := usecase.NewInteractor(func(ctx context.Context, in EnergySourceSaveInput, out *apiutil.Success[*models.EnergySource]) error {
		id, err := energyID(in.ID, "source")
		if err != nil {
			return err
		}
		s, err := energyops.SaveSourceFor(ctx, callerID(ctx), id, in.SourceInput)
		out.Data = s
		return err
	})
	return energyDoc(&u, "Update Energy Source", "Saves an energy source reading a control, device, or Home Assistant entity.")
}

func energyDeleteSource() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyIDInput, out *apiutil.Success[struct{}]) error {
		id, err := energyID(in.ID, "source")
		if err != nil {
			return err
		}
		out.Message = "Energy source deleted; its usage is kept for 30 days"
		return energyops.DeleteSource(id)
	})
	u.SetTitle("Delete Energy Source")
	u.SetDescription("Deletes an energy source. Its recorded hours are removed 30 days later.")
	u.SetTags("Energy")
	return u
}

func energyListGroups() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]energyops.GroupView]) error {
		list, err := energyops.ListGroups()
		out.Data = list
		return err
	})
	u.SetTitle("List Energy Groups")
	u.SetDescription("Lists energy groups with their current sources.")
	u.SetTags("Energy")
	return u
}

func energySaveGroup(create bool) usecase.Interactor {
	if create {
		u := usecase.NewInteractor(func(ctx context.Context, in energyops.GroupInput, out *apiutil.Success[*energyops.GroupView]) error {
			g, err := energyops.SaveGroup(uuid.Nil, in)
			out.Data = g
			return err
		})
		return energyDoc(&u, "Create Energy Group", "Saves an energy group. Membership changes apply to hours from now on.")
	}
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyGroupSaveInput, out *apiutil.Success[*energyops.GroupView]) error {
		id, err := energyID(in.ID, "group")
		if err != nil {
			return err
		}
		g, err := energyops.SaveGroup(id, in.GroupInput)
		out.Data = g
		return err
	})
	return energyDoc(&u, "Update Energy Group", "Saves an energy group. Membership changes apply to hours from now on.")
}

func energyDeleteGroup() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyIDInput, out *apiutil.Success[struct{}]) error {
		id, err := energyID(in.ID, "group")
		if err != nil {
			return err
		}
		out.Message = "Energy group deleted"
		return energyops.DeleteGroup(id)
	})
	u.SetTitle("Delete Energy Group")
	u.SetDescription("Deletes an energy group; its sources stay.")
	u.SetTags("Energy")
	return u
}

func energyListTariffs() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]*models.EnergyTariff]) error {
		list, err := energyops.ListTariffs()
		out.Data = list
		return err
	})
	u.SetTitle("List Energy Tariffs")
	u.SetDescription("Lists the house's tariffs by effective date.")
	u.SetTags("Energy")
	return u
}

func energySaveTariff(create bool) usecase.Interactor {
	if create {
		u := usecase.NewInteractor(func(ctx context.Context, in models.EnergyTariff, out *apiutil.Success[*energyops.TariffResult]) error {
			res, err := energyops.SaveTariff(uuid.Nil, in)
			out.Data = res
			return err
		})
		return energyDoc(&u, "Create Energy Tariff", "Saves a tariff and reprices the recorded hours it covers. Usage is unchanged.")
	}
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyTariffSaveInput, out *apiutil.Success[*energyops.TariffResult]) error {
		id, err := energyID(in.ID, "tariff")
		if err != nil {
			return err
		}
		res, err := energyops.SaveTariff(id, in.EnergyTariff)
		out.Data = res
		return err
	})
	return energyDoc(&u, "Update Energy Tariff", "Saves a tariff and reprices the recorded hours it covers. Usage is unchanged.")
}

func energyDeleteTariff() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyIDInput, out *apiutil.Success[map[string]int]) error {
		id, err := energyID(in.ID, "tariff")
		if err != nil {
			return err
		}
		n, err := energyops.DeleteTariff(id)
		out.Data = map[string]int{"hours_recomputed": n}
		return err
	})
	u.SetTitle("Delete Energy Tariff")
	u.SetDescription("Deletes a tariff and reprices its hours under whatever covers them now.")
	u.SetTags("Energy")
	return u
}

func energyGetSettings() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[energy.Settings]) error {
		out.Data = energyops.GetSettings()
		return nil
	})
	u.SetTitle("Get Energy Settings")
	u.SetDescription("Returns the time zone and first day of the week energy periods follow.")
	u.SetTags("Energy")
	return u
}

func energySaveSettings() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in energy.Settings, out *apiutil.Success[energy.Settings]) error {
		s, err := energyops.SaveSettings(in)
		out.Data = s
		return err
	})
	u.SetTitle("Save Energy Settings")
	u.SetDescription("Sets the time zone and first day of the week energy periods follow.")
	u.SetTags("Energy")
	return u
}

func energyRollups() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in energyops.RollupQuery, out *apiutil.Success[*energy.Rollup]) error {
		r, err := energyops.Rollups(in)
		out.Data = r
		return err
	})
	u.SetTitle("Get Energy Rollups")
	u.SetDescription("Returns a target's usage, cost, and solar figures per hour, day, week, or month.")
	u.SetTags("Energy")
	return u
}

func energyPrice() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyPriceInput, out *apiutil.Success[*energyops.PriceView]) error {
		var at time.Time
		if in.At != "" {
			var err error
			if at, err = time.Parse(time.RFC3339, in.At); err != nil {
				return apperrors.ErrValidation.WithMessage("at must be an RFC 3339 time")
			}
		}
		p, err := energyops.Price(at)
		out.Data = p
		return err
	})
	u.SetTitle("Get Energy Price")
	u.SetDescription("Returns the import price, export credit, and period in force at a time, and when it next changes.")
	u.SetTags("Energy")
	return u
}

func energyCheapest() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyCheapestInput, out *apiutil.Success[*energy.Window]) error {
		w, err := energyops.Cheapest(time.Duration(in.Hours*float64(time.Hour)), in.From, in.To)
		out.Data = w
		return err
	})
	u.SetTitle("Find Cheapest Energy Window")
	u.SetDescription("Finds the cheapest window of the given length within a range, starting on a quarter hour.")
	u.SetTags("Energy")
	return u
}

func energyPreset() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[*models.Dashboard]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized
		}
		d, err := energyops.CreatePreset(ctx, uc.UserID)
		out.Data = d
		return err
	})
	return energyDoc(&u, "Create Energy Dashboard", "Creates a dashboard of energy charts: today, this week, this month, 30 days, a breakdown by group and source, and solar when there is any.")
}

// EnergyImportInput asks for a source's history.
type EnergyImportInput struct {
	ID   string `path:"id" description:"Source UUID."`
	Days int    `json:"days" required:"true" minimum:"1" maximum:"400" description:"Days of history to read, 1 to 400."`
}

func energyImport() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EnergyImportInput, out *apiutil.Success[struct{}]) error {
		id, err := energyID(in.ID, "source")
		if err != nil {
			return err
		}
		out.Message = "History import started; its progress shows on the source"
		return energyops.ImportFor(ctx, callerID(ctx), id, in.Days)
	})
	return energyDoc(&u, "Import Energy History", "Reads up to 400 days of a source's history, from Home Assistant statistics or its forwarded history, filling hours with no live figure.")
}

// documented is an interactor that takes OpenAPI documentation.
type documented interface {
	usecase.Interactor
	SetTitle(string)
	SetDescription(string)
	SetTags(...string)
}

// energyDoc documents an interactor under the Energy tag.
func energyDoc(u documented, title, desc string) usecase.Interactor {
	u.SetTitle(title)
	u.SetDescription(desc)
	u.SetTags("Energy")
	return u
}

// registerEnergyRoutes registers energy sources, groups, tariffs, settings,
// rollups, and prices: connection read to read, management to change.
func registerEnergyRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	read := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), bearerDoc)
	write := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc)
	read.Method(http.MethodGet, "/energy/sources", nethttp.NewHandler(energyListSources()))
	read.Method(http.MethodGet, "/energy/candidates", nethttp.NewHandler(energyCandidates()))
	write.Method(http.MethodPost, "/energy/sources", nethttp.NewHandler(energySaveSource(true)))
	write.Method(http.MethodPut, "/energy/sources/{id}", nethttp.NewHandler(energySaveSource(false)))
	write.Method(http.MethodDelete, "/energy/sources/{id}", nethttp.NewHandler(energyDeleteSource()))
	write.Method(http.MethodPost, "/energy/sources/{id}/import", nethttp.NewHandler(energyImport()))
	read.Method(http.MethodGet, "/energy/groups", nethttp.NewHandler(energyListGroups()))
	write.Method(http.MethodPost, "/energy/groups", nethttp.NewHandler(energySaveGroup(true)))
	write.Method(http.MethodPut, "/energy/groups/{id}", nethttp.NewHandler(energySaveGroup(false)))
	write.Method(http.MethodDelete, "/energy/groups/{id}", nethttp.NewHandler(energyDeleteGroup()))
	read.Method(http.MethodGet, "/energy/tariffs", nethttp.NewHandler(energyListTariffs()))
	write.Method(http.MethodPost, "/energy/tariffs", nethttp.NewHandler(energySaveTariff(true)))
	write.Method(http.MethodPut, "/energy/tariffs/{id}", nethttp.NewHandler(energySaveTariff(false)))
	write.Method(http.MethodDelete, "/energy/tariffs/{id}", nethttp.NewHandler(energyDeleteTariff()))
	read.Method(http.MethodGet, "/energy/settings", nethttp.NewHandler(energyGetSettings()))
	write.Method(http.MethodPut, "/energy/settings", nethttp.NewHandler(energySaveSettings()))
	read.Method(http.MethodGet, "/energy/rollups", nethttp.NewHandler(energyRollups()))
	read.Method(http.MethodGet, "/energy/price", nethttp.NewHandler(energyPrice()))
	read.Method(http.MethodGet, "/energy/price/cheapest", nethttp.NewHandler(energyCheapest()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/energy/preset", nethttp.NewHandler(energyPreset()))
}
