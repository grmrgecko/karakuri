package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// TimeSeriesQueryInput is one query against a readable connection.
type TimeSeriesQueryInput struct {
	ID    string     `path:"id" description:"Connection UUID, or energy for energy rollups."`
	Query tsdb.Query `json:"query" required:"true" description:"Query to run."`
}

// TimeSeriesQueryHandler runs one query against a time-series connection or
// Home Assistant's recorder.
func TimeSeriesQueryHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in TimeSeriesQueryInput, out *apiutil.Success[*tsdb.Result]) error {
		res, err := tsdbops.Query(ctx, in.ID, in.Query)
		if err != nil {
			return tsdbops.AppError(err)
		}
		out.Data = res
		return nil
	})
	u.SetTitle("Query Time Series")
	u.SetDescription("Runs a PromQL, InfluxQL, Flux, SQL, or Home Assistant history or statistics query over a time range.")
	u.SetTags("Time series")
	return u
}

// TimeSeriesMetadataInput is one metadata lookup.
type TimeSeriesMetadataInput struct {
	ID          string `path:"id" description:"Connection UUID, or energy for energy targets."`
	Kind        string `query:"kind" required:"true" description:"metrics, labels, label_values, databases, measurements, tag_keys, tag_values, field_keys, schemas, tables, columns, entities, or statistic_ids."`
	Name        string `query:"name" description:"Label or tag key for label_values and tag_values."`
	Match       string `query:"match" description:"Series selector or substring narrowing the lookup."`
	Measurement string `query:"measurement" description:"InfluxDB measurement."`
	Schema      string `query:"schema" description:"PostgreSQL schema."`
	Table       string `query:"table" description:"PostgreSQL table."`
}

// TimeSeriesValues is a metadata answer.
type TimeSeriesValues struct {
	Values []string `json:"values" description:"Names found, sorted, at most 1000."`
}

// TimeSeriesMetadataHandler lists names an editor can offer.
func TimeSeriesMetadataHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in TimeSeriesMetadataInput, out *apiutil.Success[TimeSeriesValues]) error {
		values, err := tsdbops.Metadata(ctx, in.ID, tsdb.MetaRequest{
			Kind: in.Kind, Name: in.Name, Match: in.Match, Measurement: in.Measurement, Schema: in.Schema, Table: in.Table,
		})
		if err != nil {
			return tsdbops.AppError(err)
		}
		out.Data = TimeSeriesValues{Values: values}
		return nil
	})
	u.SetTitle("List Time Series Metadata")
	u.SetDescription("Lists metric, label, measurement, table, entity, or statistic names of a readable connection.")
	u.SetTags("Time series")
	return u
}

// HistoryForwardListInput optionally narrows the list to one source's own forward.
type HistoryForwardListInput struct {
	Source string `query:"source" description:"control:<id> or mqtt_device:<id> for the forward that source's editor owns."`
}

// HistoryForwardListHandler lists history forwards with destination status.
func HistoryForwardListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HistoryForwardListInput, out *apiutil.Success[[]tsdbops.ForwardItem]) error {
		items, err := tsdbops.ListForwardsFor(ctx, callerID(ctx), in.Source)
		if err != nil {
			return err
		}
		out.Data = items
		return nil
	})
	u.SetTitle("List History Forwards")
	u.SetDescription("Lists the forwards recording control, MQTT device, and Home Assistant values in time-series connections.")
	u.SetTags("Time series")
	return u
}

// HistoryForwardCreateInput is a forward to create.
type HistoryForwardCreateInput struct {
	models.HistoryForward
}

// HistoryForwardUpdateInput is a forward to replace.
type HistoryForwardUpdateInput struct {
	ForwardID string `path:"id" format:"uuid" description:"Forward UUID."`
	models.HistoryForward
}

// HistoryForwardCreateHandler creates a forward.
func HistoryForwardCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HistoryForwardCreateInput, out *apiutil.Success[tsdbops.ForwardItem]) error {
		f := in.HistoryForward
		f.ID = uuid.Nil
		item, err := tsdbops.SaveForwardFor(ctx, callerID(ctx), &f)
		if err != nil {
			return err
		}
		out.Data = item
		return nil
	})
	u.SetTitle("Create History Forward")
	u.SetDescription("Creates a forward. A forward with owner_id belongs to that control or device's editor; creating another for the same owner replaces it.")
	u.SetTags("Time series")
	return u
}

// HistoryForwardUpdateHandler replaces a forward's settings.
func HistoryForwardUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HistoryForwardUpdateInput, out *apiutil.Success[tsdbops.ForwardItem]) error {
		id, err := uuid.Parse(in.ForwardID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid forward ID")
		}
		existing, err := models.FindHistoryForward(id)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("history forward not found")
		}
		f := in.HistoryForward
		f.ID, f.CreatedAt = id, existing.CreatedAt
		item, err := tsdbops.SaveForwardFor(ctx, callerID(ctx), &f)
		if err != nil {
			return err
		}
		out.Data = item
		return nil
	})
	u.SetTitle("Update History Forward")
	u.SetDescription("Replaces a forward's settings.")
	u.SetTags("Time series")
	return u
}

// HistoryForwardDeleteInput names a forward.
type HistoryForwardDeleteInput struct {
	ID string `path:"id" format:"uuid" description:"Forward UUID."`
}

// HistoryForwardDeleteHandler removes a forward.
func HistoryForwardDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HistoryForwardDeleteInput, out *apiutil.Success[any]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid forward ID")
		}
		if err := tsdbops.DeleteForwardFor(ctx, callerID(ctx), id); err != nil {
			return err
		}
		out.Message = "History forward deleted"
		return nil
	})
	u.SetTitle("Delete History Forward")
	u.SetDescription("Stops recording what the forward selected. Points already written stay in the databases.")
	u.SetTags("Time series")
	return u
}

// HistoryStatusHandler reports every writable connection's writer.
func HistoryStatusHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]tsdbops.WriterStatus]) error {
		items, err := tsdbops.StatusesFor(ctx, callerID(ctx))
		if err != nil {
			return err
		}
		out.Data = items
		return nil
	})
	u.SetTitle("Get History Writer Status")
	u.SetDescription("Reports buffered, written, discarded, and rejected points and the last error of every writable time-series connection.")
	u.SetTags("Time series")
	return u
}

// RegisterTimeSeriesRoutes registers queries, metadata, forwards, and writer
// status.
func RegisterTimeSeriesRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	read := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), bearerDoc)
	write := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc)
	read.With(requireConnection(access.Use)).Method(http.MethodPost, "/connections/{id}/timeseries/query", nethttp.NewHandler(TimeSeriesQueryHandler()))
	read.With(requireConnection(access.Use)).Method(http.MethodGet, "/connections/{id}/timeseries/metadata", nethttp.NewHandler(TimeSeriesMetadataHandler()))
	read.Method(http.MethodGet, "/history/forwards", nethttp.NewHandler(HistoryForwardListHandler()))
	read.Method(http.MethodGet, "/history/status", nethttp.NewHandler(HistoryStatusHandler()))
	write.Method(http.MethodPost, "/history/forwards", nethttp.NewHandler(HistoryForwardCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	write.Method(http.MethodPut, "/history/forwards/{id}", nethttp.NewHandler(HistoryForwardUpdateHandler()))
	write.Method(http.MethodDelete, "/history/forwards/{id}", nethttp.NewHandler(HistoryForwardDeleteHandler()))
}
