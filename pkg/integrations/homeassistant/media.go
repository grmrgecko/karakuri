package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HistoryPoint is one recorded state of an entity.
type HistoryPoint struct {
	// T is the time of the state in Unix milliseconds.
	T int64  `json:"t"`
	V string `json:"v"`
}

// historyState is one entry of Home Assistant's compressed minimal history,
// where s is the state and lu or lc the update or change time in fractional
// Unix seconds. Later entries omit lc when it equals lu.
type historyState struct {
	S  string   `json:"s"`
	LU *float64 `json:"lu"`
	LC *float64 `json:"lc"`
}

// History returns an entity's recorded states between start and end, oldest
// first, using the recorder's history_during_period command.
func (d *Driver) History(ctx context.Context, entityID string, start, end time.Time) ([]HistoryPoint, error) {
	msg, _, err := d.command(ctx, map[string]any{
		"type":                     "history/history_during_period",
		"start_time":               start.UTC().Format(time.RFC3339),
		"end_time":                 end.UTC().Format(time.RFC3339),
		"entity_ids":               []string{entityID},
		"minimal_response":         true,
		"no_attributes":            true,
		"significant_changes_only": false,
	}, nil)
	if err != nil {
		return nil, err
	}
	var result map[string][]historyState
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid history result: %w", err)
	}
	points := make([]HistoryPoint, 0, len(result[entityID]))
	for _, s := range result[entityID] {
		at := s.LU
		if at == nil {
			at = s.LC
		}
		if at == nil {
			continue
		}
		points = append(points, HistoryPoint{T: int64(*at * 1000), V: s.S})
	}
	return points, nil
}

// AttributeHistory returns each entity's recorded states between start and
// end, oldest first. With attribute set, each point is that attribute's value
// instead of the state, and entries without it are skipped; Home Assistant
// sends attributes on every entry only without minimal_response, so the
// request asks for full entries then.
func (d *Driver) AttributeHistory(ctx context.Context, entityIDs []string, attribute string, start, end time.Time) (map[string][]HistoryPoint, error) {
	msg, _, err := d.commandWithin(ctx, 60*time.Second, map[string]any{
		"type":                     "history/history_during_period",
		"start_time":               start.UTC().Format(time.RFC3339),
		"end_time":                 end.UTC().Format(time.RFC3339),
		"entity_ids":               entityIDs,
		"minimal_response":         attribute == "",
		"no_attributes":            attribute == "",
		"significant_changes_only": false,
	}, nil)
	if err != nil {
		return nil, err
	}
	var result map[string][]struct {
		historyState
		A map[string]any `json:"a"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid history result: %w", err)
	}
	out := make(map[string][]HistoryPoint, len(result))
	for id, states := range result {
		points := make([]HistoryPoint, 0, len(states))
		for _, s := range states {
			at := s.LU
			if at == nil {
				at = s.LC
			}
			if at == nil {
				continue
			}
			v := s.S
			if attribute != "" {
				raw, ok := s.A[attribute]
				if !ok {
					continue
				}
				v = fmt.Sprint(raw)
			}
			points = append(points, HistoryPoint{T: int64(*at * 1000), V: v})
		}
		out[id] = points
	}
	return out, nil
}

// StatisticRow is one period of a long-term statistic. Fields Home Assistant
// does not return for the statistic are nil.
type StatisticRow struct {
	// Start is the period start in Unix milliseconds.
	Start  int64    `json:"start"`
	Mean   *float64 `json:"mean,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Sum    *float64 `json:"sum,omitempty"`
	State  *float64 `json:"state,omitempty"`
	Change *float64 `json:"change,omitempty"`
}

// Statistics returns long-term statistics for each ID between start and end,
// aggregated by period (5minute, hour, day, week, or month) and limited to
// the requested types.
func (d *Driver) Statistics(ctx context.Context, ids []string, period string, types []string, start, end time.Time) (map[string][]StatisticRow, error) {
	msg, _, err := d.commandWithin(ctx, 60*time.Second, map[string]any{
		"type":          "recorder/statistics_during_period",
		"start_time":    start.UTC().Format(time.RFC3339),
		"end_time":      end.UTC().Format(time.RFC3339),
		"statistic_ids": ids,
		"period":        period,
		"types":         types,
	}, nil)
	if err != nil {
		return nil, err
	}
	// Home Assistant 2023.3 and later send start as Unix milliseconds;
	// earlier releases sent an ISO string.
	var result map[string][]struct {
		Start  json.RawMessage `json:"start"`
		Mean   *float64        `json:"mean"`
		Min    *float64        `json:"min"`
		Max    *float64        `json:"max"`
		Sum    *float64        `json:"sum"`
		State  *float64        `json:"state"`
		Change *float64        `json:"change"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid statistics result: %w", err)
	}
	out := make(map[string][]StatisticRow, len(result))
	for id, rows := range result {
		list := make([]StatisticRow, 0, len(rows))
		for _, r := range rows {
			var startMs int64
			var num float64
			var text string
			if json.Unmarshal(r.Start, &num) == nil {
				startMs = int64(num)
			} else if json.Unmarshal(r.Start, &text) == nil {
				t, err := time.Parse(time.RFC3339Nano, text)
				if err != nil {
					continue
				}
				startMs = t.UnixMilli()
			} else {
				continue
			}
			list = append(list, StatisticRow{Start: startMs, Mean: r.Mean, Min: r.Min, Max: r.Max, Sum: r.Sum, State: r.State, Change: r.Change})
		}
		out[id] = list
	}
	return out, nil
}

// StatisticID describes one long-term statistic the recorder keeps.
type StatisticID struct {
	StatisticID string `json:"statistic_id"`
	Name        string `json:"name"`
	Unit        string `json:"statistics_unit_of_measurement"`
	DisplayUnit string `json:"display_unit_of_measurement"`
}

// ListStatisticIDs returns every statistic the recorder keeps.
func (d *Driver) ListStatisticIDs(ctx context.Context) ([]StatisticID, error) {
	msg, _, err := d.command(ctx, map[string]any{"type": "recorder/list_statistic_ids"}, nil)
	if err != nil {
		return nil, err
	}
	var ids []StatisticID
	if err := json.Unmarshal(msg.Result, &ids); err != nil {
		return nil, fmt.Errorf("invalid statistic ID list: %w", err)
	}
	return ids, nil
}

// TodoItem is one item of a Home Assistant to-do list.
type TodoItem struct {
	UID         string `json:"uid"`
	Summary     string `json:"summary"`
	Status      string `json:"status"`
	Due         string `json:"due,omitempty"`
	Description string `json:"description,omitempty"`
}

// TodoItems returns the items of a todo entity.
func (d *Driver) TodoItems(ctx context.Context, entityID string) ([]TodoItem, error) {
	msg, _, err := d.command(ctx, map[string]any{"type": "todo/item/list", "entity_id": entityID}, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []TodoItem `json:"items"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid todo result: %w", err)
	}
	return result.Items, nil
}

// fetchClient performs Home Assistant HTTP requests. It has no overall
// timeout because a camera stream lasts as long as its viewer; callers bound
// requests with their context.
var fetchClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Fetch performs an authenticated GET of one Home Assistant API path, such as
// /api/camera_proxy/camera.door. The path must stay under /api/ so a value
// read from an entity's attributes cannot point the connection's token at
// another host or page. The caller closes the response body.
func (d *Driver) Fetch(ctx context.Context, path string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/api/") || strings.Contains(path, "..") {
		return nil, fmt.Errorf("home assistant path %q is not an API path", path)
	}
	base := strings.TrimRight(strings.TrimSpace(d.cfg.BaseURL), "/")
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return nil, fmt.Errorf("building the home assistant URL: %w", err)
	}
	// The base URL may be written as the websocket address, which the
	// connection accepts; the HTTP API is the same host over http or https.
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building the home assistant request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching from home assistant: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("home assistant answered HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// binaryStates maps the two-valued states Home Assistant reports to numbers,
// so on/off, open/closed, and similar entities can be graphed and recorded.
var binaryStates = map[string]float64{
	"on": 1, "off": 0,
	"open": 1, "closed": 0,
	"home": 1, "not_home": 0,
	"locked": 1, "unlocked": 0,
	"detected": 1, "clear": 0,
	"true": 1, "false": 0,
}

// NumericState reads a state as a number: a numeric state, or 1 or 0 for a
// two-valued state such as on and off.
func NumericState(state string) (float64, bool) {
	if v, err := strconv.ParseFloat(state, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v, true
	}
	v, ok := binaryStates[strings.ToLower(state)]
	return v, ok
}
